package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/pluginhelp/externalplugins"
	"sigs.k8s.io/yaml"
)

const pluginName = "observer"

type Config struct {
	BigQuery struct {
		CredentialFile string `json:"credentials"`
		ProjectID      string `json:"projectID"`
		Dataset        string `json:"dataset"`
	} `json:"bigquery"`
	OrgRepos map[string]ManagedOrgRepo `json:"orgsRepos,omitempty"`
}

// ManagedOrgRepo is used by the Observer plugin to represent an Organisation
type ManagedOrgRepo struct {
	Projects map[int]ManagedProject `json:"projects,omitempty"`
	// with a list of Columns
}

// ManagedProject is used by the Observer plugin to represent a Project
type ManagedProject struct {
	ExcludeColumns []string `json:"excludeColumns,omitempty"`
}

type options struct {
	port       int
	dryRun     bool
	github     prowflagutil.GitHubOptions
	hmacSecret string
	configPath string
}

func (o *options) Validate() error {
	for _, group := range []flagutil.OptionGroup{&o.github} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}

	return nil
}

func newOptions() *options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8787, "Port to listen on.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing (uses API tokens but does not mutate).")
	fs.StringVar(&o.hmacSecret, "hmac", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.configPath, "config", "/etc/config/config.yaml", "Path to the plugin config")

	for _, group := range []flagutil.OptionGroup{&o.github} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])

	return &o
}

func helpProvider(enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `Observes cards in a project and stores their metadata in BigQuery`,
	}
	return pluginHelp, nil
}

func main() {
	o := newOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	logrus.SetFormatter(&logrus.JSONFormatter{})
	// todo(leodido) > use global option from the Prow config.
	logrus.SetLevel(logrus.DebugLevel)
	log := logrus.StandardLogger().WithField("plugin", pluginName)

	b, err := os.ReadFile(o.configPath)
	if err != nil {
		log.Fatalf("Invalid config %s: %v", o.configPath, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		log.Fatalf("error unmarshaling %s: %v", o.configPath, err)
	}
	log.WithField("config", cfg).Info("loaded config")

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.github.TokenPath, o.hmacSecret}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	githubClient, err := o.github.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	ctx, cancel := context.WithCancel(context.Background())
	bq, err := bigquery.NewClient(ctx, cfg.BigQuery.ProjectID, option.WithCredentialsFile(cfg.BigQuery.CredentialFile))
	if err != nil {
		logrus.WithError(err).Fatal("Error getting BigQuery client.")
	}
	err = setupBigQuery(ctx, bq, cfg.BigQuery.Dataset)
	if err != nil {
		logrus.WithError(err).Fatal("Error setting up BigQuery session.")
	}

	mux := http.NewServeMux()
	externalplugins.ServeExternalPluginHelp(mux, log, helpProvider)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}

	health := pjutil.NewHealth()
	health.ServeReady()
	defer interrupts.WaitForGracefulShutdown()
	defer cancel()

	tbl := bq.Dataset(cfg.BigQuery.Dataset).Table(tableName)

	var wg sync.WaitGroup
	defer wg.Wait()
	for orgRepo, repoCfg := range cfg.OrgRepos {
		for projectNumber, prj := range repoCfg.Projects {
			wg.Add(1)
			go observeRepo(ctx, &wg, tbl, githubClient, orgRepo, projectNumber, prj)
		}
	}

	interrupts.ListenAndServe(httpServer, 5*time.Second)
}

type ProjectColumnEntry struct {
	Project   string
	Issue     int
	Title     string
	Column    string
	Timestamp time.Time
	Assignees []string
	Labels    []string
}

const tableName = "prj_col_entry"

func setupBigQuery(ctx context.Context, client *bigquery.Client, dataset string) error {
	schema, err := bigquery.InferSchema(ProjectColumnEntry{})
	if err != nil {
		return err
	}
	ds := client.Dataset(dataset)
	err = ds.Create(ctx, nil)
	if err != nil && !isAlreadyExists(err) {
		return err
	}

	tbl := ds.Table(tableName)
	err = tbl.Create(ctx, &bigquery.TableMetadata{Schema: schema})
	if err != nil && !isAlreadyExists(err) {
		return err
	}

	return nil
}

func isAlreadyExists(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		if gerr.Code == http.StatusConflict {
			return true
		}
	}
	return false
}

// {
// 	organization(login: "gitpod-io") {
// 		project(number: 3) {
// 		columns(first: 100) {
// 			nodes {
// 			name
// 			cards(first: 100) {
// 				nodes {
// 				content {
// 					... on Issue {
// 					id
// 					number
// 					title
// 					labels (first:10) {
// 						nodes {
// 						name
// 						}
// 					}
// 					}
// 					... on PullRequest {
// 					id
// 					number
// 					title
// 					}
// 				}
// 				}
// 			}
// 			}
// 		}
// 		}
// 	}
// }

type queryIssue struct {
	Number githubql.Int
	Title  githubql.String
	Labels struct {
		Nodes []struct {
			Name githubql.String
		}
	} `graphql:"labels(first: 10)"`
	Assignees struct {
		Nodes []struct {
			Login githubql.String
		}
	} `graphql:"assignees(first: 5)"`
}

type projectQuery struct {
	Organisation struct {
		Project struct {
			Name    githubql.String
			Columns struct {
				Nodes []struct {
					Name  string
					Cards struct {
						Nodes []struct {
							Content struct {
								PR    queryIssue `graphql:"... on PullRequest"`
								Issue queryIssue `graphql:"... on Issue"`
							}
						}
					} `graphql:"cards(first: 100)"`
				}
			} `graphql:"columns(first: 6)"`
		} `graphql:"project(number: $prj)"`
	} `graphql:"organization(login: $org)"`
}

func observeRepo(ctx context.Context, wg *sync.WaitGroup, tbl *bigquery.Table, gh github.Client, org string, projectNumber int, prj ManagedProject) {
	defer wg.Done()

	ins := tbl.Inserter()
	vars := map[string]interface{}{
		"prj": githubql.Int(projectNumber),
		"org": githubql.String(org),
	}

	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for ; true; <-tick.C {
		if ctx.Err() != nil {
			break
		}

		now := time.Now()

		var q projectQuery
		qctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := gh.Query(qctx, &q, vars)
		cancel()
		if err != nil {
			logrus.WithError(err).WithFields(vars).Error("cannot query project")
			continue
		}

		for _, col := range q.Organisation.Project.Columns.Nodes {
			var excluded bool
			for _, c := range prj.ExcludeColumns {
				if c == col.Name {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}

			for _, crd := range col.Cards.Nodes {
				var issue queryIssue
				if crd.Content.Issue.Title != "" {
					issue = crd.Content.Issue
				} else if crd.Content.PR.Title != "" {
					issue = crd.Content.PR
				} else {
					continue
				}

				assignees := make([]string, len(issue.Assignees.Nodes))
				for i := range issue.Assignees.Nodes {
					assignees[i] = string(issue.Assignees.Nodes[i].Login)
				}

				labels := make([]string, len(issue.Labels.Nodes))
				for i := range issue.Labels.Nodes {
					labels[i] = string(issue.Labels.Nodes[i].Name)
				}

				err = ins.Put(ctx, ProjectColumnEntry{
					Project:   string(q.Organisation.Project.Name),
					Issue:     int(issue.Number),
					Title:     string(issue.Title),
					Column:    col.Name,
					Timestamp: now,
					Assignees: assignees,
					Labels:    labels,
				})
				if err != nil {
					logrus.WithError(err).Warn("cannot insert into bigquery")
				}
			}
		}
	}
}
