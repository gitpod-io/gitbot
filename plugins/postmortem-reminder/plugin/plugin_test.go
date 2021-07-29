package plugin

import (
	"testing"
	"k8s.io/test-infra/prow/github"
)

type mockGithubClient struct {
	labels []string
	state  string
	issueCommented bool
}

func newGithubClient(labels []string, state string) *mockGithubClient{
	return &mockGithubClient{
		labels: labels,
		state: state,
		issueCommented: false,
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
		
	}
}