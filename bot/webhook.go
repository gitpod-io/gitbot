package bot

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v33/github"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const (
	labelReporterFeedbackNeeded = "reporter-feedback-needed"
)

func (b *Bot) handleGithubWebhook(w http.ResponseWriter, r *http.Request) {
	var err error
	defer func() {
		if err == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
	}()

	payload, err := github.ValidatePayload(r, []byte(b.Config.GitHub.WebhookSecret))
	if err != nil {
		logrus.WithError(err).Error("invalid GitHub payload")
		return
	}

	evt, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		logrus.WithError(err).Error("cannot parse GitHub event")
		return
	}

	switch evt := evt.(type) {
	case *github.IssueCommentEvent:
		err = b.handleGithubIssueCommentEvent(r.Context(), evt)
	case *github.IssueEvent:
		err = b.handleGithubIssueEvent(r.Context(), evt)
	}
	if err != nil {
		logrus.WithError(err).Error("cannot handle GitHub event")
		return
	}
}

func (b *Bot) handleGithubIssueCommentEvent(ctx context.Context, evt *github.IssueCommentEvent) error {
	err := b.handleReporterFeedbackNeeded(ctx, evt)
	if err != nil {
		return err
	}

	return nil
}

func (b *Bot) handleReporterFeedbackNeeded(ctx context.Context, evt *github.IssueCommentEvent) error {
	lbl, ok := hasLabel(evt.Issue.Labels, labelReporterFeedbackNeeded)
	if !ok {
		return nil
	}

	if evt.Comment.GetAuthorAssociation() == evt.Issue.GetAuthorAssociation() {
		// owner responeded, remove the label
		var (
			repo    = evt.GetRepo()
			owner   = repo.GetOwner().GetLogin()
			repoN   = repo.GetName()
			issueNr = evt.Issue.GetNumber()
		)

		_, err := b.ghClient.Issues.RemoveLabelForIssue(ctx, owner, repoN, issueNr, lbl)
		if err != nil {
			logrus.WithError(err).WithField("issue", evt.Issue.GetURL()).Warn("cannot remove reporter-feedback-needed label")
		}

		evt.Issue.GetEventsURL()
		// TODO(cw): use pagination
		issueEvts, _, err := b.ghClient.Issues.ListIssueEvents(ctx, owner, repoN, issueNr, &github.ListOptions{PerPage: 100})
		if err != nil {
			return err
		}
		var (
			latestEvt        *time.Time
			requestingGHUser string
		)
		for _, ie := range issueEvts {
			if ie.GetEvent() != "labeled" {
				continue
			}
			if _, ok := hasLabel([]*github.Label{ie.Label}, labelReporterFeedbackNeeded); ok {
				continue
			}
			if latestEvt != nil && ie.CreatedAt.Before(*latestEvt) {
				continue
			}
			latestEvt = ie.CreatedAt
			requestingGHUser = ie.GetActor().GetLogin()
		}
		if requestingGHUser == "" {
			logrus.WithField("issue", evt.GetIssue().GetURL()).Warn("someone requested reporter feedback, but can't figure out who")
		} else {
			slackUser := requestingGHUser
			if u, ok := b.Config.Slack.GitHubToSlackUser[slackUser]; ok {
				slackUser = u
			}
			_, _, _, err = b.slackClient.SendMessageContext(ctx, "@"+slackUser,
				slack.MsgOptionAsUser(true),
				slack.MsgOptionText("An issue reporter provided feedback", false),
				slack.MsgOptionBlocks(
					slack.NewActionBlock("link",
						slack.NewButtonBlockElement("view_issue", evt.Issue.GetURL(),
							slack.NewTextBlockObject("mrkdwn", "View issue", false, true),
						),
					),
				),
			)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (b *Bot) handleGithubIssueEvent(ctx context.Context, evt *github.IssueEvent) error {
	switch *evt.Event {
	case "labeled":
	case "unlabeled":
	}
	return nil
}

func hasLabel(lbl []*github.Label, name string) (actualName string, ok bool) {
	for _, l := range lbl {
		if l == nil {
			continue
		}
		if !strings.Contains(l.GetName(), name) {
			continue
		}

		return l.GetName(), true
	}
	return "", false
}
