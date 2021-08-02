package plugin

type Config struct {
	OrgRepos map[string]RepoConfig `json:"orgRepos" yaml:"orgRepos"`
}

type RepoConfig struct {
	slackWebhookUrl string `json:"slackWebhookUrl" yaml:"slackWebhookUrl"`
}