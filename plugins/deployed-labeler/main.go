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
}

func newOptions() *options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8080, "Port to listen to.")
	fs.BoolVar(&o.dryRun, "dry-run", false, "Dry run for testing (uses API tokens but does not mutate).")
	fs.StringVar(&o.hmacSecret, "hmac", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

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
	logrus.SetLevel(logrus.DebugLevel)
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
	switch req.Method {
	case "POST":
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
			w.WriteHeader(http.StatusPreconditionFailed)
			w.Write([]byte(http.StatusText(http.StatusPreconditionFailed)))
			s.log.Errorf("team and commit are required parameters. Team: %v, commit: %v", team, commitSHA)
			break
		}

		var msg struct {
			DeployedPRs struct {
				Team []string `json:"team"`
				All  []string `json:"all"`
			} `json:"deployedPRs"`
			Errors []error `json:"errors"`
		}
		msg.DeployedPRs.Team, msg.DeployedPRs.All, msg.Errors = s.handleMarkDeployedPRs(req.Context(), commitSHA, team)

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
			continue
		}

		teamDeployedLabel := deployedLabel(team)
		if _, hasLabel := pr.Labels[teamDeployedLabel]; !hasLabel {
			teamDeployed = append(teamDeployed, pr.URL)
			err := s.gh.AddLabel(org, repo, pr.Number, teamDeployedLabel)
			if err != nil {
				errs = append(errs, err)
			} else {
				pr.Labels[teamDeployedLabel] = struct{}{}
			}
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
			err := s.gh.AddLabel(org, repo, pr.Number, labelDeployed)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	return
}

func deployedLabel(team string) string { return fmt.Sprintf("%s: %s", labelDeployed, team) }
func teamLabel(team string) string     { return labelPrefixTeam + team }

// getMergedPRs returns a query object which contains 100 commits and its associated PRs
// that are present in the default branch's commit tree, as long as they were merged before the commit
// passed as an argument.
func (s *server) getMergedPRs(ctx context.Context, commitSHA string) ([]pullRequest, error) {
	variables := map[string]interface{}{
		"org":  githubv4.String(org),
		"repo": githubv4.String(repo),
		// We always want to start the cursor at the given commit
		"commitCursor": githubv4.String(fmt.Sprintf("%s %s", commitSHA, "0")),
	}

	var q query

	err := s.gh.Query(ctx, &q, variables)
	if err != nil {
		s.log.WithError(err).Error("Error running query.")
		return nil, err
	}

	commits := q.Organization.Repository.DefaultBranchRef.Target.Commit.History.Nodes
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
			DefaultBranchRef struct {
				Target struct {
					Commit struct {
						History struct {
							Nodes []struct {
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
						} `graphql:"history(first: 100, after: $commitCursor)"`
					} `graphql:"... on Commit"`
				}
			}
		} `graphql:"repository(name: $repo)"`
	} `graphql:"organization(login: $org)"`
}
