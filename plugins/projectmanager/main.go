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

// TODO(cw): remove cards from the project if they don't match any of the columns

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gitpod-io/gitbot/common"
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

// ProjectManager represents the config for the ProjectManager plugin, holding top
// level config options, configuration is a hierarchial structure with top level element
// being org or org/repo with the list of projects as its children
type ProjectManager struct {
	OrgRepos map[string]ManagedOrgRepo `json:"orgsRepos,omitempty"`
}

// ManagedOrgRepo is used by the ProjectManager plugin to represent an Organisation
// or Repository with a list of Projects
type ManagedOrgRepo struct {
	FromRepos []string                  `json:"fromRepos,omitempty"`
	Projects  map[string]ManagedProject `json:"projects,omitempty"`
}

// ManagedProject is used by the ProjectManager plugin to represent a Project
// with a list of Columns
type ManagedProject struct {
	Columns []ManagedColumn `json:"columns,omitempty"`
}

// ManagedColumn is used by the ProjectQueries plugin to represent a project column
// and the conditions to add a PR to that column
type ManagedColumn struct {
	// Either of ID or Name should be specified
	ID *int `json:"id,omitempty"`
	// State must be open, closed or all
	State string `json:"state,omitempty"`
	// all the labels here should match to the incoming event to be bale to add the card to the project
	Labels []string `json:"labels,omitempty"`
	// Configuration is effective is the issue events repo/Owner/Login matched the org
	Org string `json:"org,omitempty"`
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

	b, err := os.ReadFile(o.projectMgrConfig)
	if err != nil {
		log.Fatalf("Invalid config %s: %v", o.projectMgrConfig, err)
	}
	var mgrCfg ProjectManager
	if err := yaml.Unmarshal(b, &mgrCfg); err != nil {
		log.Fatalf("error unmarshaling %s: %v", o.projectMgrConfig, err)
	}
	log.WithField("config", mgrCfg).Info("loaded config")

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.github.TokenPath, o.hmacSecret}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	githubClient, err := o.github.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	serv := &server{
		eventTokenGenerator:  secretAgent.GetTokenGenerator(o.hmacSecret),
		githubTokenGenerator: secretAgent.GetTokenGenerator(o.github.TokenPath),
		gh:                   githubClient,
		log:                  log,
		mgrCfg:               mgrCfg,
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
	eventTokenGenerator  func() []byte
	githubTokenGenerator func() []byte
	configAgent          *config.Agent
	gh                   github.Client
	log                  *logrus.Entry
	mgrCfg               ProjectManager
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := github.ValidateWebhook(w, r, s.eventTokenGenerator)
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

	return s.updateMatchingColumn(ie, l)
}

func (s *server) updateMatchingColumn(ie github.IssueEvent, log *logrus.Entry) error {
	gc := s.gh
	orgRepos := s.mgrCfg.OrgRepos

	e := eventData{
		id:     ie.Issue.ID,
		number: ie.Issue.Number,
		isPR:   ie.Issue.IsPullRequest(),
		org:    ie.Repo.Owner.Login,
		repo:   ie.Repo.Name,
		state:  ie.Issue.State,
		labels: ie.Issue.Labels,
		remove: ie.Action == github.IssueActionUnlabeled,
	}

	var err error
	// Don't use GetIssueLabels unless it's required and keep track of whether the labels have been fetched to avoid unnecessary API usage.
	if len(e.labels) == 0 {
		e.labels, err = gc.GetIssueLabels(e.org, e.repo, e.number)
		if err != nil {
			log.Infof("Cannot get labels for issue/PR: %d, error: %s", e.number, err)
		}
	}

	for orgRepoName, managedOrgRepo := range orgRepos {
		var matchesFromRepo bool
		for _, r := range managedOrgRepo.FromRepos {
			if r == e.org+"/"+e.repo {
				matchesFromRepo = true
				break
			}
		}
		if !matchesFromRepo && len(managedOrgRepo.FromRepos) > 0 {
			continue
		}

		for projectName, managedProject := range managedOrgRepo.Projects {
			for _, managedColumn := range managedProject.Columns {
				if managedColumn.ID == nil {
					log.Infof("Ignoring column: {%v}, has no columnID", managedColumn, e.number, e.org)
				}
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

				cardID, err := common.FindCardByIssueURL(gc, orgRepoName, projectName, e.number, *managedColumn.ID)
				if err != nil {
					log.Infof("Cannot add the issue/PR: %d to the project: %s, column: %s, error: %s", e.number, projectName, managedColumn.ID, err)
					break
				}

				if cardID == nil {
					err = addIssueToColumn(gc, *managedColumn.ID, e)
				} else {
					err = common.MoveProjectCard(s.githubTokenGenerator, e.org, *cardID, *managedColumn.ID, "bottom")
				}
				if err != nil {
					log.WithError(err).WithFields(logrus.Fields{
						"matchedColumnID": *managedColumn.ID,
					}).Error(failedToAddProjectCard)
				}

				break
			}
		}
	}
	return err
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
