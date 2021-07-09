/*
Copyright 2019 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// external plugin version of https://github.com/kubernetes/test-infra/blob/c6b7db8ad0fa9b8fc4f20eff7151ff76fa85b623/prow/plugins/projectmanager/projectmanager.go

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/pluginhelp/externalplugins"
	"k8s.io/test-infra/prow/plugins"
)

const pluginName = "project-manager"

var (
	failedToAddProjectCard = "Failed to add project card for the issue/PR"
	issueAlreadyInProject  = "The issue/PR %s already assigned to the project %s"

	handleIssueActions = map[github.IssueEventAction]bool{
		github.IssueActionOpened:    true,
		github.IssueActionReopened:  true,
		github.IssueActionLabeled:   true,
		github.IssueActionUnlabeled: true,
	}
)

type options struct {
	port             int
	dryRun           bool
	github           prowflagutil.GitHubOptions
	hmacSecret       string
	projectMgrConfig string
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
	fs.StringVar(&o.projectMgrConfig, "mgr-config", "/etc/project-manager", "Path to the project manager config")

	for _, group := range []flagutil.OptionGroup{&o.github} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])

	return &o
}

func helpProvider(enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The project-manager plugin automatically adds Pull Requests to specified GitHub Project Columns, if the label on the PR matches with configured project and the column.`,
	}
	return pluginHelp, nil
}

func main() {
	o := newOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	b, err := os.ReadFile(o.projectMgrConfig)
	if err != nil {
		logrus.Fatalf("Invalid config %s: %v", o.projectMgrConfig, err)
	}
	var mgrCfg plugins.ProjectManager
	if err := yaml.Unmarshal(b, mgrCfg); err != nil {
		logrus.Fatalf("error unmarshaling %s: %v", o.projectMgrConfig, err)
	}

	logrus.SetFormatter(&logrus.JSONFormatter{})
	// todo(leodido) > use global option from the Prow config.
	logrus.SetLevel(logrus.DebugLevel)
	log := logrus.StandardLogger().WithField("plugin", pluginName)

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.github.TokenPath, o.hmacSecret}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	githubClient, err := o.github.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	serv := &server{
		tokenGenerator: secretAgent.GetTokenGenerator(o.hmacSecret),
		gh:             githubClient,
		log:            log,
		mgrCfg:         mgrCfg,
	}

	health := pjutil.NewHealth()

	log.Info("Complete serve setting")
	mux := http.NewServeMux()
	mux.Handle("/", serv)
	externalplugins.ServeExternalPluginHelp(mux, log, helpProvider)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}

	health.ServeReady()

	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)
}

type server struct {
	tokenGenerator func() []byte
	configAgent    *config.Agent
	gh             github.Client
	log            *logrus.Entry
	mgrCfg         plugins.ProjectManager
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := github.ValidateWebhook(w, r, s.tokenGenerator)
	if !ok {
		return
	}
	// Event received, handle it
	if err := s.handleEvent(eventType, eventGUID, payload); err != nil {
		logrus.WithError(err).Error("Error parsing event.")
	}
}

func (s *server) handleEvent(eventType, eventGUID string, payload []byte) error {
	l := s.log.WithFields(logrus.Fields{
		"event-type":     eventType,
		github.EventGUID: eventGUID,
	})

	switch eventType {
	case "issues":
		var ic github.IssueEvent
		if err := json.Unmarshal(payload, &ic); err != nil {
			return err
		}
		go func() {
			if err := s.handleIssueOrPullRequest(ic, l); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event.")
			}
		}()
	default:
		logrus.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

// Strict subset of *github.Client methods.
type githubClient interface {
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	GetRepoProjects(owner, repo string) ([]github.Project, error)
	GetOrgProjects(org string) ([]github.Project, error)
	GetProjectColumns(org string, projectID int) ([]github.ProjectColumn, error)
	GetColumnProjectCards(org string, columnID int) ([]github.ProjectCard, error)
	CreateProjectCard(org string, columnID int, projectCard github.ProjectCard) (*github.ProjectCard, error)
	MoveProjectCard(org string, projectCardID int, newColumnID int) error
}

type eventData struct {
	id     int
	number int
	isPR   bool
	org    string
	repo   string
	state  string
	labels []github.Label
	remove bool
}

type DuplicateCard struct {
	projectName string
	issueURL    string
}

func (m *DuplicateCard) Error() string {
	return fmt.Sprintf(issueAlreadyInProject, m.issueURL, m.projectName)
}

func (s *server) handleIssueOrPullRequest(ie github.IssueEvent, l *logrus.Entry) error {
	if !handleIssueActions[ie.Action] {
		return nil
	}
	eventData := eventData{
		id:     ie.Issue.ID,
		number: ie.Issue.Number,
		isPR:   ie.Issue.IsPullRequest(),
		org:    ie.Repo.Owner.Login,
		repo:   ie.Repo.Name,
		state:  ie.Issue.State,
		labels: ie.Issue.Labels,
		remove: ie.Action == github.IssueActionUnlabeled,
	}

	return handle(s.gh, s.mgrCfg, l, eventData)
}

func handle(gc githubClient, projectManager plugins.ProjectManager, log *logrus.Entry, e eventData) error {
	return updateMatchingColumn(gc, projectManager.OrgRepos, e, log)
}

func updateMatchingColumn(gc githubClient, orgRepos map[string]plugins.ManagedOrgRepo, e eventData, log *logrus.Entry) error {
	var err error
	// Don't use GetIssueLabels unless it's required and keep track of whether the labels have been fetched to avoid unnecessary API usage.
	if len(e.labels) == 0 {
		e.labels, err = gc.GetIssueLabels(e.org, e.repo, e.number)
		if err != nil {
			log.Infof("Cannot get labels for issue/PR: %d, error: %s", e.number, err)
		}
	}

	issueURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%v", e.org, e.repo, e.number)
	for orgRepoName, managedOrgRepo := range orgRepos {
		for projectName, managedProject := range managedOrgRepo.Projects {
			for _, managedColumn := range managedProject.Columns {
				// Org is not specified or does not match we just ignore processing this column
				if managedColumn.Org == "" || managedColumn.Org != e.org {
					log.Infof("Ignoring column: {%v}, for issue/PR: %d, due to org: %v", managedColumn, e.number, e.org)
					continue
				}
				// If state is not matching we ignore processing this column
				// If state is empty then it defaults to 'open'
				if managedColumn.State != "" && managedColumn.State != e.state {
					log.Infof("Ignoring column: {%v}, for issue/PR: %d, due to state: %v", managedColumn, e.number, e.state)
					continue
				}

				// if labels do not match we continue to the next project
				// if labels are empty on the column, the match should return false
				if !github.HasLabels(managedColumn.Labels, e.labels) {
					log.Infof("Ignoring column: {%v}, for issue/PR: %d, labels due to labels: %v ", managedColumn, e.number, e.labels)
					continue
				}

				var (
					cardID   *int
					columnID *int
				)

				var err error
				columnID, cardID, err = getColumnID(gc, orgRepoName, projectName, managedColumn.Name, issueURL)
				if err != nil {
					log.Infof("Cannot add the issue/PR: %d to the project: %s, column: %s, error: %s", e.number, projectName, managedColumn.Name, err)
					break
				}
				if columnID == nil {
					continue
				}

				if cardID == nil {
					err = addIssueToColumn(gc, *columnID, e)
				} else {
					err = gc.MoveProjectCard(e.org, *cardID, *columnID)
				}
				if err != nil {
					log.WithError(err).WithFields(logrus.Fields{
						"matchedColumnID": *columnID,
					}).Error(failedToAddProjectCard)
				}

				break
			}
		}
	}
	return err
}

// getColumnID returns a column id only if the issue if the project and column name provided are valid
// and the issue is not already in the project
func getColumnID(gc githubClient, orgRepoName, projectName, columnName, issueURL string) (col *int, existingCard *int, err error) {
	var projects []github.Project
	orgRepoParts := strings.Split(orgRepoName, "/")
	switch len(orgRepoParts) {
	case 2:
		projects, err = gc.GetRepoProjects(orgRepoParts[0], orgRepoParts[1])
	case 1:
		projects, err = gc.GetOrgProjects(orgRepoParts[0])
	default:
		err = fmt.Errorf("could not determine org or org/repo from %s", orgRepoName)
		return
	}

	if err != nil {
		return
	}

	for _, project := range projects {
		if project.Name == projectName {
			columns, err := gc.GetProjectColumns(orgRepoParts[0], project.ID)
			if err != nil {
				return nil, nil, err
			}

			for _, column := range columns {
				cards, err := gc.GetColumnProjectCards(orgRepoParts[0], column.ID)
				if err != nil {
					return nil, nil, err
				}

				for _, card := range cards {
					if card.ContentURL == issueURL {
						cid := card.ID
						existingCard = &cid
					}
				}
			}
			for _, column := range columns {
				if column.Name == columnName {
					return &column.ID, existingCard, nil
				}
			}
			return nil, nil, fmt.Errorf("could not find column %s in project %s", columnName, projectName)
		}
	}
	return nil, nil, fmt.Errorf("could not find project %s in org/repo %s", projectName, orgRepoName)
}

func addIssueToColumn(gc githubClient, columnID int, e eventData) error {
	// Create project card and add this PR
	projectCard := github.ProjectCard{}
	if e.isPR {
		return nil
	}

	projectCard.ContentType = "Issue"
	projectCard.ContentID = e.id
	_, err := gc.CreateProjectCard(e.org, columnID, projectCard)
	return err
}
