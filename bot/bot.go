package bot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/v33/github"
	"github.com/slack-go/slack"
)

// Config configures GitBot
type Config struct {
	GitHub struct {
		AppID          int64  `yaml:"appID"`
		InstallationID int64  `yaml:"installationID"`
		PrivateKeyPath string `yaml:"privateKey"`
		WebhookSecret  string `yaml:"webhookSecret"`
	} `yaml:"github"`
	Webhook struct {
		Addr string `yaml:"address"`
	} `yaml:"webhook"`
	Slack struct {
		APIToken          string            `yaml:"token"`
		GitHubToSlackUser map[string]string `yaml:"ghUserMap"`
	} `yaml:"slack"`
}

// Bot implements gitbot
type Bot struct {
	Config      Config
	ghClient    *github.Client
	slackClient *slack.Client
	activeOn    []repoName
	stop        chan struct{}
}

type repoName struct {
	Owner string
	Name  string
}

// New creates a new bot
func New(cfg Config) (*Bot, error) {
	ghtp, err := ghinstallation.NewKeyFromFile(http.DefaultTransport, cfg.GitHub.AppID, cfg.GitHub.InstallationID, cfg.GitHub.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("GitHub config error: %w", err)
	}
	_, err = ghtp.Token(context.Background())
	if err != nil {
		return nil, fmt.Errorf("GitHub config error: %w", err)
	}
	rs, err := ghtp.Repositories()
	if err != nil {
		return nil, fmt.Errorf("GitHub config error: %w", err)
	}
	repos := make([]repoName, len(rs))
	for i, r := range rs {
		repos[i] = repoName{
			Name:  r.GetName(),
			Owner: r.GetOwner().GetLogin(),
		}
	}

	ghclient := github.NewClient(&http.Client{
		Transport: ghtp,
		Timeout:   30 * time.Second,
	})

	slackClient := slack.New(cfg.Slack.APIToken)

	return &Bot{
		Config:      cfg,
		ghClient:    ghclient,
		slackClient: slackClient,
		activeOn:    repos,
		stop:        make(chan struct{}),
	}, nil
}

// Run runs the bot and returns if there is a severe error
// or the bot was stopped.
func (b *Bot) Run() error {
	l, err := net.Listen("tcp", b.Config.Webhook.Addr)
	if err != nil {
		return err
	}

	go b.serveWebhook(l)
	go b.maintainStaleIssues()
	<-b.stop

	return nil
}

func (b *Bot) serveWebhook(l net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("/github", http.HandlerFunc(b.HandleGithubWebhook))
	http.Serve(l, mux)

	<-b.stop
}
