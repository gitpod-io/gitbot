package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
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
		mergedPrs, errors := s.handleMarkDeployedPRs(commitSHA, team)
		msg := struct {
			MergedPullRequests []string `json:"mergedPullRequests"`
			Errors             []error  `json:"errors"`
		}{
			MergedPullRequests: mergedPrs,
			Errors:             errors,
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
func (s *server) handleMarkDeployedPRs(commitSHA, team string) (mergedPrs []string, errors []error) {
	containsTeamLabel := false

	q, err := s.getMergedPRs(commitSHA)
	if err != nil {
		return nil, append(errors, err)
	}

	for _, mergedCommits := range q.Organization.Repository.DefaultBranchRef.Target.Commit.History.Nodes {
		for _, associatedPRs := range mergedCommits.AssociatedPullRequests.Nodes {
			containsTeamLabel = false

			for _, label := range associatedPRs.Labels.Nodes {
				if containsTeamLabel = label.Name == githubv4.String(fmt.Sprintf("team: %s", team)); containsTeamLabel {
					err := s.gh.AddLabel("gitpod-io", "gitpod", int(associatedPRs.Number), fmt.Sprintf("deployed: %s", team))
					if err != nil {
						errors = append(errors, err)
					} else {
						mergedPrs = append(mergedPrs, string(associatedPRs.URL))
					}

					break
				}
			}
		}
	}
	return mergedPrs, nil
}

// getMergedPRs returns a query object which contains 100 commits and its associated PRs
// that are present in the default branch's commit tree, as long as they were merged before the commit
// passed as an argument.
func (s *server) getMergedPRs(commitSHA string) (query, error) {
	variables := map[string]interface{}{
		"org":  githubv4.String("gitpod-io"),
		"repo": githubv4.String("gitpod"),
		// We always want to start the cursor at the given commit
		"commitCursor": githubv4.String(fmt.Sprintf("%s %s", commitSHA, "0")),
	}

	var q query

	err := s.gh.Query(context.TODO(), &q, variables)
	if err != nil {
		s.log.WithError(err).Error("Error running query.")
		return q, err
	}
	return q, nil
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
