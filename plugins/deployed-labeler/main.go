package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/test-infra/pkg/flagutil"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"

	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
)

const pluginName = "deployed-marker"

type options struct {
	port       int
	dryRun     bool
	github     prowflagutil.GitHubOptions
	hmacSecret string
	logLevel   string
}

func newOptions() *options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8080, "Port to listen to.")
	fs.BoolVar(&o.dryRun, "dry-run", false, "Dry run for testing (uses API tokens but does not mutate).")
	fs.StringVar(&o.hmacSecret, "hmac", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.logLevel, "log-level", "debug", "Application log level")

	for _, group := range []flagutil.OptionGroup{&o.github} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])

	return &o
}

type server struct {
	eventTokenGenerator  func() []byte
	githubTokenGenerator func() []byte
	gh                   github.Client
	log                  *logrus.Entry
}

func main() {
	o := newOptions()

	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.ParseLevel(o.logLevel)
	log := logrus.StandardLogger().WithField("plugin", pluginName)

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.github.TokenPath, o.hmacSecret}); err != nil {
		log.WithError(err).Fatal("Error starting secrets agent.")
	}
	githubClient, err := o.github.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		log.WithError(err).Fatal("Error getting GitHub client.")
	}

	serv := &server{
		eventTokenGenerator:  secretAgent.GetTokenGenerator(o.hmacSecret),
		githubTokenGenerator: secretAgent.GetTokenGenerator(o.github.TokenPath),
		gh:                   githubClient,
		log:                  log,
	}

	health := pjutil.NewHealth()
	log.Info("Complete serve setting")

	mux := http.NewServeMux()
	mux.HandleFunc("/deployed", serv.markDeployedPR)
	httpServer := &http.Server{Addr: ":8080", Handler: mux}

	health.ServeReady()

	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)

}

func (s *server) markDeployedPR(w http.ResponseWriter, req *http.Request) {
	var commitSHA string
	var team string
	for k, v := range req.URL.Query() {
		switch k {
		case "commit":
			commitSHA = v[0]
		case "team":
			team = v[0]
		default:
			s.log.Warnf("Unrecognized parameter received: %s", k)
		}
	}

	if team == "" || commitSHA == "" {
		s.log.WithFields(
			logrus.Fields{
				"team":   team,
				"commit": commitSHA,
			},
		).Error("team and commit are required parameters")

		w.WriteHeader(http.StatusPreconditionFailed)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]string{
			"error": "team and commit are required parameters",
		})
		return
	}
	var err error
	switch req.Method {
	case "GET":
		var msg struct {
			PRs struct {
				Labeled   []string `json:"labeled"`
				Unlabeled []string `json:"unlabeled"`
			}
			Error string `json:"error"`
		}

		msg.PRs.Labeled, msg.PRs.Unlabeled, err = s.handleGetUnmarkedPRs(req.Context(), commitSHA, team)
		if err != nil {
			msg.Error = err.Error()
			w.WriteHeader(http.StatusUnprocessableEntity)
		}

		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(msg)

	case "POST":
		var errs []error
		var msg struct {
			DeployedPRs struct {
				Team []string `json:"team"`
				All  []string `json:"all"`
			} `json:"deployedPRs"`
			Errors []string `json:"errors"`
		}
		msg.DeployedPRs.Team, msg.DeployedPRs.All, errs = s.handleMarkDeployedPRs(req.Context(), commitSHA, team)

		msg.Errors = make([]string, 0, len(errs))
		for _, err := range errs {
			msg.Errors = append(msg.Errors, err.Error())
		}

		if len(msg.Errors) > 0 {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}

		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(msg)
	default:
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
	}
}

// handleMarkDeployedPRs adds the 'deployed: <team>' label to pull requests that are associated to merged commits,
// as long as they have the label 'team: <team>' .
func (s *server) handleMarkDeployedPRs(ctx context.Context, commitSHA, team string) (teamDeployedPRs, allDeployedPRs []string, errors []error) {
	prs, err := s.getMergedPRs(ctx, commitSHA)
	if err != nil {
		return nil, nil, []error{err}
	}

	return s.updatePullRequests(prs, team)
}

func (s *server) handleGetUnmarkedPRs(ctx context.Context, commitSHA, team string) (labeledPRs, unlabeledPRs []string, err error) {
	prs, err := s.getMergedPRs(ctx, commitSHA)
	if err != nil {
		return nil, nil, err
	}

	labeledPRs, unlabeledPRs = s.findTeamPullRequests(prs, team)

	return labeledPRs, unlabeledPRs, nil
}

const (
	org             = "gitpod-io"
	repo            = "gitpod"
	labelDeployed   = "deployed"
	labelPrefixTeam = "team: "
)

func (s *server) updatePullRequests(prs []pullRequest, team string) (teamDeployed []string, allDeployed []string, errs []error) {
	for _, pr := range prs {

		lblTeam := teamLabel(team)
		if _, belongs := pr.Labels[lblTeam]; !belongs {
			s.log.Infof("PR %v does not belong to %v, skipping it", pr.Number, team)
			continue
		}

		teamDeployedLabel := deployedLabel(team)
		if _, hasLabel := pr.Labels[teamDeployedLabel]; !hasLabel {
			teamDeployed = append(teamDeployed, pr.URL)
			s.log.Infof("Adding %v label to %v", teamDeployedLabel, pr.Number)
			err := s.gh.AddLabel(org, repo, pr.Number, teamDeployedLabel)
			if err != nil {
				errs = append(errs, err)
			} else {
				pr.Labels[teamDeployedLabel] = struct{}{}
			}
		} else {
			s.log.Infof("PR %v already has label %v", pr.Number, teamDeployedLabel)
		}

		allTeamsDeployed := true
		for lbl := range pr.Labels {
			if strings.HasPrefix(lbl, labelPrefixTeam) {
				team := strings.TrimPrefix(lbl, labelPrefixTeam)
				if _, deployed := pr.Labels[deployedLabel(team)]; !deployed {
					allTeamsDeployed = false
				}
			}
		}
		if _, hasLabel := pr.Labels[labelDeployed]; allTeamsDeployed && !hasLabel {
			allDeployed = append(allDeployed, pr.URL)
			s.log.Infof("Adding %v label to %v", labelDeployed, pr.Number)
			err := s.gh.AddLabel(org, repo, pr.Number, labelDeployed)
			if err != nil {
				errs = append(errs, err)
			}
		} else {
			s.log.Infof("PR %v already has label %v", pr.Number, labelDeployed)
		}
	}
	return
}

// Find labeled and unlabeled pull requests for the given team.
func (s *server) findTeamPullRequests(prs []pullRequest, team string) (labeled, unlabeled []string) {
	for _, pr := range prs {

		lblTeam := teamLabel(team)
		if _, belongs := pr.Labels[lblTeam]; !belongs {
			s.log.Infof("PR %v does not belong to %v, skipping it", pr.Number, team)
			continue
		}

		teamDeployedLabel := deployedLabel(team)
		if _, hasLabel := pr.Labels[teamDeployedLabel]; !hasLabel {
			unlabeled = append(unlabeled, pr.URL)
		} else {
			labeled = append(labeled, pr.URL)
		}
	}
	return
}

func deployedLabel(team string) string { return fmt.Sprintf("%s: %s", labelDeployed, team) }
func teamLabel(team string) string     { return labelPrefixTeam + team }

// getMergedPRs returns a query object which contains X commits and its associated PRs
// that are present in the default branch's commit tree, as long as they were merged before the commit
// passed as an argument.
func (s *server) getMergedPRs(ctx context.Context, commitSHA string) ([]pullRequest, error) {
	s.log.Infof("Fetching merged PRs for commit %v", commitSHA)
	variables := map[string]interface{}{
		"org":    githubv4.String(org),
		"repo":   githubv4.String(repo),
		"commit": githubv4.GitObjectID(commitSHA),
		"cursor": (*githubv4.String)(nil), // null gets the first page
	}

	var q query
	var commits []commitNodes

	// we get 10 commits per page
	// 10x50 = 500 in total
	//
	// Note that this value is sensitive to the commit rate of the given repo and the interval at which teams deploy;
	// if more than 500 commits are merged within a week and a given team only deploys on a weekly basis then some commits
	// might not be labeled.
	for i := 0; i < 50; i++ {
		err := s.gh.Query(ctx, &q, variables)
		if err != nil {
			s.log.WithField("query", q).WithError(err).Error("Error running query.")
			return nil, err
		}

		commits = append(commits, q.Organization.Repository.Object.Commit.History.Nodes...)
		if !q.Organization.Repository.Object.Commit.History.PageInfo.HasNextPage {
			break
		}
		variables["cursor"] = q.Organization.Repository.Object.Commit.History.PageInfo.EndCursor
	}

	res := make([]pullRequest, 0, len(commits))
	for _, c := range commits {
		for _, rpr := range c.AssociatedPullRequests.Nodes {
			pr := pullRequest{
				Number: int(rpr.Number),
				URL:    string(rpr.URL),
				Labels: make(map[string]struct{}, len(rpr.Labels.Nodes)),
			}
			for _, lbl := range rpr.Labels.Nodes {
				pr.Labels[string(lbl.Name)] = struct{}{}
			}
			res = append(res, pr)

			s.log.WithFields(
				logrus.Fields{
					"Number": pr.Number,
					"URL":    pr.URL,
					"Labels": pr.Labels,
				},
			).Info("Added pull request for commit %s", c)
		}
	}

	return res, nil
}

type pullRequest struct {
	Number int
	URL    string
	Labels map[string]struct{}
}
type query struct {
	Organization struct {
		Repository struct {
			Object struct {
				Commit struct {
					History struct {
						PageInfo struct {
							EndCursor   githubv4.String
							HasNextPage bool
						}
						Nodes []commitNodes
					} `graphql:"history(first: 10, after: $cursor)"`
				} `graphql:"... on Commit"`
			} `graphql:"object(oid: $commit)"`
		} `graphql:"repository(name: $repo)"`
	} `graphql:"organization(login: $org)"`
}
type commitNodes struct {
	AssociatedPullRequests struct {
		Nodes []struct {
			Number githubv4.Int
			URL    githubv4.String
			Labels struct {
				Nodes []struct {
					Name githubv4.String
				}
			} `graphql:"labels(first: 10)"`
		}
	} `graphql:"associatedPullRequests(first: 2)"`
}
