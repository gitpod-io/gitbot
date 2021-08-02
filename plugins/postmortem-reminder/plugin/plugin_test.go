package plugin

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/github"
)

type mockGithubClient struct {
	labels               []string
	issue                *github.Issue
	issueCommented       bool
	lastCommentTimestamp time.Time
	log                  *logrus.Entry
}

func newGithubClient(labels []string, issue *github.Issue) *mockGithubClient {
	return &mockGithubClient{
		labels:               labels,
		issue:                issue,
		issueCommented:       false,
		lastCommentTimestamp: time.Now(),
		log:                  &logrus.Entry{},
	}
}

func (gh *mockGithubClient) GetIssueLabels(org, repo, id) ([]string, error) {
	return gh.labels, nil
}

func (gh *mockGithubClient) CommentReminder(org, repo, id) error {
	gh.issueCommented = true
	return nil
}

func TestHandle(t *testing.T) {
	issue := func() *github.Issue {
		issue := github.Issue{
			ID: 1,
		}
		return &issue
	}

	testCases := []struct {
		name                 string
		issue                *github.Issue
		labels               []string
		lastCommentTimestamp time.Time
		state                string

		expectComment bool
	}{
		{
			name: "No issue, ignoring",
		},
		{
			name:                 "Closed issue, no labels, no-op",
			issue:                issue(),
			labels:               []string{},
			lastCommentTimestamp: time.Now(),
			state:                "closed",
			expectComment:        false,
		},
		{
			name:                 "Closed issue, with label, no-op",
			issue:                issue(),
			labels:               []string{defaultPostMortemLabel},
			lastCommentTimestamp: time.Now(),
			state:                "closed",
			expectComment:        false,
		},
		{
			name:                 "Open issue, no post-mortem label, no-op",
			issue:                issue(),
			labels:               []string{"groundwork: scheduled"},
			lastCommentTimestamp: time.Now(),
			state:                "open",
			expectComment:        false,
		},
		{
			name:                 "Open issue with post-mortem label, but recent comment(less than a week), no-op",
			issue:                issue(),
			labels:               []string{"groundwork: scheduled", defaultPostMortemLabel},
			lastCommentTimestamp: time.Now().Add(time.Duration(-6) * (time.Hour * 24)),
			state:                "open",
			expectComment:        false,
		},
		{
			name:                 "Open issue with post-mortem label and no recent comment(more than a week), expect comment",
			issue:                issue(),
			labels:               []string{defaultPostMortemLabel, "groundwork: scheduled"},
			lastCommentTimestamp: time.Now().Add(time.Duration(-8) * (time.Hour * 24)),
			state:                "open",
			expectComment:        true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			mockGh := newGithubClient(testCase.labels, testCase.issue)
			issueEvent := &github.IssueEvent{}
			if testCase.issue != nil {
				issueEvent.Issue = *testCase.issue
				testCase.issue.State = testCase.state
				// testCase.issue.Labels = testCase.labels // Prow has it's own label type... how do we add custom labels?
			}
			if err := handle(mockGh.log, issueEvent); err != nil {
				t.Fatalf("error handling issue event: %v", err)
			}

			// TODO:
			// mockGh.compareExpected()

		})
	}
}
