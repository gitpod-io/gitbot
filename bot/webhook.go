package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v33/github"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

const (
	labelReporterFeedbackNeeded = "reporter-feedback-needed"
)

func (b *Bot) HandleGithubWebhook(w http.ResponseWriter, r *http.Request) {
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
	type F func(context.Context, *github.IssueCommentEvent) error
	fs := []F{
		b.handleReporterFeedbackNeeded,
		b.handleEffortEstimate,
	}

	for _, f := range fs {
		err := f(ctx, evt)
		if err != nil {
			return err
		}
	}

	return nil
}

// EffortEstimate contains the lower, med, and upper bound of an effort estimate
type EffortEstimate struct {
	Min *float64 `json:"min,omitempty"`
	Med *float64 `json:"med,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

func parseEffortEstimate(msg string) (est *EffortEstimate, err error) {
	est = &EffortEstimate{}
	lines := strings.Split(msg, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "/effort ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "/effort")), 32)
			if err != nil {
				return nil, err
			}
			est.Med = &v
			continue
		}
		if strings.HasPrefix(l, "min ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "min")), 32)
			if err != nil {
				return nil, err
			}
			est.Min = &v
			continue
		}
		if strings.HasPrefix(l, "max ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "max")), 32)
			if err != nil {
				return nil, err
			}
			est.Max = &v
			continue
		}
	}
	return
}

func (b *Bot) handleEffortEstimate(ctx context.Context, evt *github.IssueCommentEvent) (err error) {
	cmt := evt.GetComment()
	body := cmt.GetBody()
	if !strings.Contains(body, "/effort") {
		return
	}

	defer func() {
		if err != nil {
			body = strings.ReplaceAll(body, "/effort", "effort")
			body = fmt.Sprintf(":X: %s\n\n---\n%s", err.Error(), body)
		}

		cmt.Body = &body
		_, _, err = b.ghClient.Issues.EditComment(ctx, evt.GetRepo().GetOwner().GetLogin(), evt.GetRepo().GetName(), evt.GetComment().GetID(), evt.GetComment())
	}()

	est, err := parseEffortEstimate(body)
	if err != nil {
		return err
	}

	if est.Med == nil || *est.Med <= 0 {
		return fmt.Errorf("most likely estimate must be greater than zero")
	}
	if est.Min != nil && *est.Min <= 0 {
		return fmt.Errorf("min must be greater than zero")
	}
	if est.Min != nil && *est.Min >= *est.Med {
		return fmt.Errorf("min must be less than most likely estimate")
	}
	if est.Max != nil && *est.Max <= *est.Med {
		return fmt.Errorf("max must be greater than most likely estimate")
	}

	eb, err := json.MarshalIndent(est, "", "  ")
	if err != nil {
		return err
	}
	body = fmt.Sprintf("<details><summary>Effort Estimate counted. Thank You :+1:</summary><pre>\n%s\n</pre></details>", string(eb))

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
