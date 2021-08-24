package main

import (
	"fmt"
)

const (
	defaultTemplate = "{{.Greeting}} @{{.AuthorLogin}}! 👋"
)

var greetings = [4]string{"Benvenuto", "Welcome", "Willkommen", "Bienvenido"}

type pullRequestInfo struct {
	Greeting    string
	Org         string
	Repo        string
	AuthorLogin string
	AuthorName  string
	Type        string
}

type Config struct {
	OrgsRepos map[string]RepoConfig `json:"orgsRepos" yaml:"orgsRepos"`
}

type RepoConfig struct {
	Label   string `json:"label" yaml:"label"`
	Message string `json:"message" yaml:"message"`
}

func (c Config) getMessage(org, repo string) string {
	k := fmt.Sprintf("%s/%s", org, repo)
	rc, ok := c.OrgsRepos[k]
	if !ok {
		// Fallback to organization only config
		rc, ok = c.OrgsRepos[org]
		if !ok {
			// Fallback to catch-all config
			rc, ok = c.OrgsRepos[""]
			if !ok {
				// Fallback to default
				rc.Message = defaultTemplate
			}
		}
	}

	return rc.Message
}

func (c Config) getLabel(org, repo string) string {
	k := fmt.Sprintf("%s/%s", org, repo)
	rc, ok := c.OrgsRepos[k]
	if !ok {
		// Fallback to organization only config
		rc, ok = c.OrgsRepos[org]
		if !ok {
			// Fallback to catch-all config
			rc, ok = c.OrgsRepos[""]
			if !ok {
				// Fallback to default
				rc.Label = ""
			}
		}
	}

	return rc.Label
}
