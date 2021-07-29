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
	labelPostMortem = "post-mortem: action item"
)

var sleep = time.Sleep

// HelpProvider constructs the PluginHelp for the postmotem-reminder plugin that takes into account enabled repositories.
// HelpProvider defines the type for function that construct the PluginHelp for plugins.
func HelpProvider(_ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	return &pluginHelp.PluginHelp{
		Description: `The postmortem-reminder plugin do periodic reminders that issues labeled with 'post-mortem action-item' needs to be solved to prevent new incidents, or that need to be closed in case the action-item is not relevant anymore.`
	}, nil
}

// handle handles a Github Issue to determine if it needs to be reminded or not.
func handle(log *logrus.Entry, gh github.Client, issue *github.Issue) error {
	labels, err := gh.GetIssueLabels(org, repo, issue.ID)
	if err != nil {
		return err
	}

	//TODO(arthursens): handle the issue
}