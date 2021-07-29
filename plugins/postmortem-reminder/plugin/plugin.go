package plugin

import (
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/labels"
	"k8s.io/test-infra/prow/github"
)

const (
	PluginName = "postmortem-reminder"
	reminderMessage = "We're susceptible to another incident while this issue isn't solved. Please solve it or close if it doesn't make sense anymore."
	defaultPostMortemLabel = "post-mortem: action item"
)

type pluginInterface interface {
	GetIssueLabels(org, repo, id) ([]string, error)
	CommentReminder(org, repo) error
}

var sleep = time.Sleep

type Config struct {
	OrgsRepos map[string]RepoConfig `json:"orgsRepos"`
}

type RepoConfig struct {
	slackWebhookUrl string `json:"slackWebhookUrl"`
	postMortemLabel string `json:"postMortemLabel"`
}

type server struct {
	tokenGenerator       func() []byte
	githubTokenGenerator func() []byte
	gh                   github.Client
	log                  *logrus.Entry
	cfg                  Config
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {

}

// handleEvent ignores all github events but 'issues' event.
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
			if err := s.handle(l, ic); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event.")
			}
		}()
	default:
		logrus.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

// HelpProvider constructs the PluginHelp for the postmotem-reminder plugin that takes into account enabled repositories.
// HelpProvider defines the type for function that construct the PluginHelp for plugins.
func HelpProvider(_ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	return &pluginHelp.PluginHelp{
		Description: `The postmortem-reminder plugin do periodic reminders that issues labeled with 'post-mortem action-item' needs to be solved to prevent new incidents, or that need to be closed in case the action-item is not relevant anymore.`
	}, nil
}

// handle handles a Github Issue to determine if it needs to be reminded or not.
func (s *server) handle(log *logrus.Entry, issue github.IssueEvent) error {
	//TODO(arthursens): handle the issue
	return nil
}