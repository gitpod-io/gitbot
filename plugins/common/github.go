package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/github"
)

func FindCardByIssueURL(gh github.Client, org, repo string, number int, col int) (cardID *int, err error) {
	issueURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%v", org, repo, number)
	card, err := gh.GetColumnProjectCard(org, col, issueURL)
	if err != nil {
		return
	}
	if card != nil {
		cardID = &card.ID
	}
	return
}

func MoveProjectCard(tokenGenerator func() []byte, org string, cardID, columnID int, position string) error {
	reqParams := struct {
		Position string `json:"position"`
		ColumnID int    `json:"column_id"`
	}{position, columnID}
	body, err := json.Marshal(reqParams)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("https://api.github.com/projects/columns/cards/%d/moves", cardID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader(tokenGenerator))
	req.Header.Set("Accept", "application/vnd.github.inertia-preview+json")
	// Disable keep-alive so that we don't get flakes when GitHub closes the
	// connection prematurely.
	// https://go-review.googlesource.com/#/c/3210/ fixed it for GET, but not
	// for POST.
	req.Close = true

	client := http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf("moveProjectCard: non-20x GitHub response (%d): %s", resp.StatusCode, string(b))
	}

	return nil
}

func CountColumnCardsWithLabel(gh github.Client, org string, projectNumber int, column int, label string) (int, error) {
	type queryIssue struct {
		Labels struct {
			Nodes []struct {
				Name githubql.String
			}
		} `graphql:"labels(first: 10)"`
	}
	type projectQuery struct {
		Organisation struct {
			Project struct {
				Columns struct {
					Nodes []struct {
						DatabaseID githubql.Int
						Cards      struct {
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

	vars := map[string]interface{}{
		"prj": githubql.Int(projectNumber),
		"org": githubql.String(org),
	}

	var q projectQuery
	qctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	err := gh.Query(qctx, &q, vars)
	cancel()
	if err != nil {
		return 0, err
	}

	var count int
	for _, c := range q.Organisation.Project.Columns.Nodes {
		if int(c.DatabaseID) != column {
			continue
		}

		logrus.WithField("col", c).Debug("CountColumnCardsWithLabel: found column")
		for _, card := range c.Cards.Nodes {
			for _, l := range append(card.Content.PR.Labels.Nodes, card.Content.Issue.Labels.Nodes...) {
				if string(l.Name) == label {
					count++
					break
				}
			}
		}
		break
	}
	return count, nil
}

func authHeader(tokenSource func() []byte) string {
	if tokenSource == nil {
		return ""
	}
	token := tokenSource()
	if len(token) == 0 {
		return ""
	}
	return fmt.Sprintf("Bearer %s", token)
}
