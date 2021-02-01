package bot

import (
	"context"
	"time"

	"github.com/google/go-github/v33/github"
	"github.com/sirupsen/logrus"
)

const (
	staleMaintainanceInterval = 30 * time.Minute
	timeUntilStale            = 90 * 24 * time.Hour
	timeUntilClose            = 30 * 24 * time.Hour
	staleLabel                = "meta: stale"

	noClose = true
)

func (b *Bot) maintainStaleIssues() {
	t := time.NewTicker(staleMaintainanceInterval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
		case <-b.stop:
			break
		}

		ctx, cancel := context.WithTimeout(context.Background(), staleMaintainanceInterval/2)
		defer cancel()

		for _, repo := range b.activeOn {
			var page int
			for {
				issues, resp, err := b.ghClient.Issues.ListByRepo(ctx, repo.Owner, repo.Name, &github.IssueListByRepoOptions{
					State:     "open",
					Sort:      "updated",
					Direction: "asc",
					ListOptions: github.ListOptions{
						Page:    page,
						PerPage: 100,
					},
				})
				if err != nil {
					logrus.WithError(err).Error("cannot list repo issue")
					break
				}
				for _, issue := range issues {
					if issue.Milestone != nil {
						continue
					}

					var hasLabel bool
					for _, l := range issue.Labels {
						if l.GetName() == staleLabel {
							hasLabel = true
							break
						}
					}

					age := time.Since(issue.GetUpdatedAt())
					if age > timeUntilStale && !hasLabel {
						log := logrus.WithField("issue", issue.GetURL())
						_, _, err := b.ghClient.Issues.AddLabelsToIssue(ctx, repo.Owner, repo.Name, issue.GetNumber(), []string{staleLabel})
						if err == nil {
							log.Info("added stale label")
						} else {
							log.WithError(err).Warn("cannot add stale label")
						}
						continue
					}
					if hasLabel && age > timeUntilClose {
						log := logrus.WithField("issue", issue.GetURL())
						if noClose {
							log.Info("would have closed this issue if it weren't for noClose")
						}

						closed := "closed"
						_, _, err := b.ghClient.Issues.Edit(ctx, repo.Owner, repo.Name, issue.GetNumber(), &github.IssueRequest{
							State: &closed,
						})
						if err == nil {
							log.Info("closed stale issue")
						} else {
							log.WithError(err).Warn("cannot close stale issue")
						}
						continue
					}
				}
				if resp.NextPage == 0 {
					break
				} else {
					page = resp.NextPage
				}
			}
		}
	}
}
