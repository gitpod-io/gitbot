package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gitpod-io/gitbot/common"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	prowgithub "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
)

var (
	commentRegex            = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	genericLabelRegex       = regexp.MustCompile(`(?m)^/label\s*(.*?)\s*$`)
	genericRemoveLabelRegex = regexp.MustCompile(`(?m)^/remove-label\s*(.*?)\s*$`)
)

type matchers struct {
	labelRegex       *regexp.Regexp
	removeLabelRegex *regexp.Regexp
}

type server struct {
	tokenGenerator       func() []byte
	githubTokenGenerator func() []byte
	gh                   prowgithub.Client
	log                  *logrus.Entry
	cfg                  Config
	repoMatchers         map[string]matchers
	// repos -> { "<key>: <value>", ... }
	repoValidLabels map[string]sets.String
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := prowgithub.ValidateWebhook(w, r, s.tokenGenerator)
	if !ok {
		// todo(leodido) > log
		return
	}
	// Event received, handle it
	if err := s.handleEvent(eventType, eventGUID, payload); err != nil {
		s.log.WithError(err).Error("Error parsing event")
	}
}

func (s *server) handleEvent(eventType, eventGUID string, payload []byte) error {
	l := s.log.WithFields(logrus.Fields{
		"event-type":         eventType,
		prowgithub.EventGUID: eventGUID,
	})

	var evt prowgithub.GenericCommentEvent

	switch eventType {
	case "issue_comment":
		if err := json.Unmarshal(payload, &evt); err != nil {
			return err
		}
		go func() {
			if err := s.handle(&evt); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event")
			}
		}()
	case "issues":
		if err := json.Unmarshal(payload, &evt); err != nil {
			return err
		}
		go func() {
			if err := s.handle(&evt); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event")
			}
		}()
	case "pull_request":
		if err := json.Unmarshal(payload, &evt); err != nil {
			return err
		}
		go func() {
			if err := s.handle(&evt); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event")
			}
		}()
	default:
		s.log.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

func (s *server) handle(e *prowgithub.GenericCommentEvent) error {
	// Remove comments from the body
	bodyWithoutComments := commentRegex.ReplaceAllString(e.Body, "")

	// Select regular expressions depending on the current repository
	labelRegex := s.repoMatchers[e.Repo.FullName].labelRegex
	removeLabelRegex := s.repoMatchers[e.Repo.FullName].removeLabelRegex

	// Search for specific matches like /<key> <target> and /remove-<key> <target>
	labelMatches := labelRegex.FindAllStringSubmatch(bodyWithoutComments, -1)
	removeLabelMatches := removeLabelRegex.FindAllStringSubmatch(bodyWithoutComments, -1)

	// Search for generic /label <target> and /remove-label <target> matches
	genericLabelMatches := genericLabelRegex.FindAllStringSubmatch(bodyWithoutComments, -1)
	genericRemoveLabelMatches := genericRemoveLabelRegex.FindAllStringSubmatch(bodyWithoutComments, -1)

	// Is this an issue or a pull request?
	what := "issue"
	if e.IsPR {
		what = "pull request"
	}

	if len(labelMatches) == 0 && len(removeLabelMatches) == 0 && len(genericLabelMatches) == 0 && len(genericRemoveLabelMatches) == 0 {
		s.log.Debugf("no label command found in %s %s#%d", what, e.Repo.FullName, e.Number)
		return nil
	}

	org := e.Repo.Owner.Login
	repo := e.Repo.Name

	repoLabels, err := s.gh.GetRepoLabels(org, repo)
	if err != nil {
		return err
	}
	// FIXME(leodido) > Get Labels for PULL REQUEST!
	labels, err := s.gh.GetIssueLabels(org, repo, e.Number)
	if err != nil {
		return err
	}
	existingRepoLabels := sets.String{}
	for _, l := range repoLabels {
		existingRepoLabels.Insert(strings.ToLower(l.Name))
	}

	// Support variables
	existent := s.repoValidLabels[e.Repo.FullName]
	var nonexistent []string
	var noSuchLabelsOnIssue []string

	// Get labels to add and labels to remove
	labelsToAdd := append(getLabelsFromMatches(labelMatches, existent, &nonexistent), getLabelsFromGenericMatches(genericLabelMatches, existent, &nonexistent)...)
	labelsToRemove := append(getLabelsFromMatches(removeLabelMatches, existent, &nonexistent), getLabelsFromGenericMatches(genericRemoveLabelMatches, existent, &nonexistent)...)

	// Add labels
	for _, addition := range labelsToAdd {
		if prowgithub.HasLabel(addition, labels) {
			s.log.WithField("label", addition).Debug("label already present: nothing to add")
			continue
		}

		if !existingRepoLabels.Has(addition) {
			s.log.WithField("label", addition).WithField("repo", e.Repo.FullName).Debug("missing label: adding it to the repository")
			if err := s.gh.AddRepoLabel(org, repo, addition, "", fmt.Sprintf("#%s", common.RandStringBytesMaskImprSrc(6))); err != nil {
				s.log.WithError(err).WithField("label", addition).WithField("repo", e.Repo.FullName).Debug("failure creating label")
				continue
			}
		}

		// TODO > implement here custom access control depending on the user (e.User.Login)
		// Example: /type: <target> only available to user that are members of the org (need a field in the configuration)
		// if addition ==  ... { s.gh.IsMember(org, e.User.Login) ... }

		if err := s.gh.AddLabel(org, repo, e.Number, addition); err != nil {
			s.log.WithError(err).Errorf("GitHub failed to add the label to %s %s#%d", what, e.Repo.FullName, e.Number)
		}
	}

	// Remove labels
	for _, remove := range labelsToRemove {
		if !prowgithub.HasLabel(remove, labels) {
			s.log.WithField("label", remove).Info("label not present: nothing to remove")
			noSuchLabelsOnIssue = append(noSuchLabelsOnIssue, remove)
			continue
		}

		if !existingRepoLabels.Has(remove) {
			s.log.WithField("label", remove).WithField("repo", e.Repo.FullName).Debug("missing label: repository does not have it")
			continue
		}

		// TODO > implement here custom access control depending on the user (e.User.Login)

		if err := s.gh.RemoveLabel(org, repo, e.Number, remove); err != nil {
			s.log.WithError(err).Errorf("GitHub failed to remove the label from %s %s#%d", what, e.Repo.FullName, e.Number)
		}
	}

	if len(nonexistent) > 0 {
		s.log.Infof("Not existing labels: %v", nonexistent)

		msg := fmt.Sprintf("The label(s) `%s` cannot be applied. These labels are supported: `%s`", strings.Join(nonexistent, ", "), strings.Join(existent.List(), ", "))

		// TODO > make our own FormatResponseRaw and use it here

		return s.gh.CreateComment(org, repo, e.Number, plugins.FormatResponseRaw(bodyWithoutComments, e.HTMLURL, e.User.Login, msg))
	}

	// The user tried to remove labels that were not present
	if len(noSuchLabelsOnIssue) > 0 {
		msg := fmt.Sprintf("Those labels are not set on the %s: `%v`", what, strings.Join(noSuchLabelsOnIssue, ", "))

		// TODO > make our own FormatResponseRaw and use it here

		return s.gh.CreateComment(org, repo, e.Number, plugins.FormatResponseRaw(bodyWithoutComments, e.HTMLURL, e.User.Login, msg))
	}

	return nil
}

// Get the labels from regexp matches
func getLabelsFromMatches(matches [][]string, valids sets.String, invalids *[]string) (labels []string) {
	for _, match := range matches {
		m := strings.TrimSpace(match[0])
		for _, label := range strings.Split(m, " ")[1:] {
			label = strings.ToLower(match[1] + ": " + strings.Trim(strings.TrimSpace(label), "\""))
			if valids.Has(label) {
				labels = append(labels, label)
			} else {
				*invalids = append(*invalids, label)
			}
		}
	}
	return
}

// Get the labels from generic matches
func getLabelsFromGenericMatches(matches [][]string, valids sets.String, invalids *[]string) []string {
	var labels []string
	for _, match := range matches {
		parts := strings.Split(strings.TrimSpace(match[0]), " ")
		if parts[0] != "/label" && parts[0] != "/remove-label" {
			continue
		}
		candidate := strings.ToLower(strings.Trim(strings.TrimSpace(parts[1]), "\""))
		if valids.Has(candidate) {
			labels = append(labels, candidate)
		} else {
			*invalids = append(*invalids, candidate)
		}
	}
	return labels
}
