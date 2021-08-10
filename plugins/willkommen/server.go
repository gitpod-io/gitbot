package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"math/rand"
	"net/http"

	"github.com/sirupsen/logrus"
	prowgithub "k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/plugins"
	"k8s.io/test-infra/prow/plugins/trigger"
)

type server struct {
	tokenGenerator     func() []byte
	gh                 prowgithub.Client
	log                *logrus.Entry
	cfg                Config
	pluginsConfigAgent *plugins.ConfigAgent
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, code := prowgithub.ValidateWebhook(w, r, s.tokenGenerator)
	if !ok {
		s.log.WithField("status code", code).Error("Error validating webhook")
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

	switch eventType {
	case "issue_comment":
		fallthrough
	case "pull_request":
		var evt prowgithub.PullRequestEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			s.log.WithError(err).WithFields(l.Data).Error("Error unmarshaling event")
			return err
		}
		go func() {
			if err := s.handlePullRequestEvent(&evt); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event")
			}
		}()
	default:
		s.log.WithFields(l.Data).Debugf("Skipping event")
	}

	return nil
}

func (s *server) handlePullRequestEvent(evt *prowgithub.PullRequestEvent) error {
	if evt.Action != prowgithub.PullRequestActionOpened {
		s.log.Debug("only consider newly opened pull requests")
		return nil
	}

	if evt.PullRequest.User.Type != prowgithub.UserTypeUser {
		s.log.Debug("ignore pull requests opened by bots")
		return nil
	}

	pluginsConfig := s.pluginsConfigAgent.Config()
	if pluginsConfig == nil {
		return fmt.Errorf("missing plugins config")
	}

	org := evt.PullRequest.Base.Repo.Owner.Login
	repo := evt.PullRequest.Base.Repo.Name
	user := evt.PullRequest.User.Login

	t := pluginsConfig.TriggerFor(org, repo)
	trustedResponse, err := trigger.TrustedUser(s.gh, t.OnlyOrgMembers, org, user, org, repo)
	if err != nil {
		return fmt.Errorf("check if user %s is trusted: %v", user, err)
	}
	if trustedResponse.IsTrusted {
		s.log.Debug("ignore trusted users")
		return nil
	}

	query := fmt.Sprintf("is:pr repo:%s/%s author:%s", org, repo, user)
	res, err := s.gh.FindIssues(query, "", false)
	if err != nil {
		return err
	}

	// In case there are no results, this is the first...
	if len(res) == 0 || len(res) == 1 && res[0].Number == evt.Number {
		welcomeTemplate := s.cfg.getMessage(org, repo)
		// load the template, and run it over the PR info
		parsedTemplate, err := template.New("welcome").Parse(welcomeTemplate)
		if err != nil {
			return err
		}
		var msgBuffer bytes.Buffer
		err = parsedTemplate.Execute(&msgBuffer, pullRequestInfo{
			Greeting:    greetings[rand.Intn(len(greetings))],
			Org:         org,
			Repo:        repo,
			AuthorLogin: user,
			AuthorName:  evt.PullRequest.User.Name,
		})
		if err != nil {
			return err
		}

		// Comment
		err = s.gh.CreateComment(org, repo, evt.PullRequest.Number, msgBuffer.String())
		if err != nil {
			return err
		}

		// Set label
		if lbl := s.cfg.getLabel(org, repo); lbl != "" {
			err = s.gh.AddLabel(org, repo, evt.PullRequest.Number, lbl)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
