package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"math/rand"
    "time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/pluginhelp/externalplugins"
)

const (
	pluginName      = "willkommen"
)

func getHelpProvider(orgRepos map[string]RepoConfig) externalplugins.ExternalPluginHelpProvider {
	return func(enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
		welcomeConfig := map[string]string{}
		for _, orgrepo := range enabledRepos {
			k := ""
			if orgrepo.Org != "" {
				if orgrepo.Repo != "" {
					k = orgrepo.String()
				} else {
					k = orgrepo.Org
				}
			}

			c, ok := orgRepos[k]
			if !ok && k != "" {
				return nil, fmt.Errorf("no config found for %s", k)
			}
			if k == "" {
				c.Message = defaultTemplate
				k = "all repositories"
			}
			welcomeConfig[k] = fmt.Sprintf("Welcomes external contributors to %s by posting with the following welcome template: %q.", k, c.Message)
			if c.Label != "" {
				welcomeConfig[k] += fmt.Sprintf(" It also adds the label %q to the pull request.", c.Label)
			}
		}

		return &pluginhelp.PluginHelp{
				Description: "The welcome plugin posts a welcoming message when it detects a user's first contribution to a repository.",
				Config:      welcomeConfig,
			},
			nil
	}
}

func main() {
	rand.Seed(time.Now().Unix())

	o := newOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	logrus.SetFormatter(&logrus.JSONFormatter{})
	// TODO(leodido) > use global option from the Prow config.
	logrus.SetLevel(logrus.DebugLevel)
	log := logrus.StandardLogger().WithField("plugin", pluginName)

	// Read plugin config
	data, err := os.ReadFile(o.config)
	if err != nil {
		log.Fatalf("Invalid configuration %s: %v", o.config, err)
	}

	var conf Config
	if err := yaml.Unmarshal(data, &conf); err != nil {
		log.Fatalf("Error unmarshaling %s: %v", o.config, err)
	}
	log.WithField("config", conf).Info("Configuration loaded")

	pca, err := o.pluginsConfig.PluginAgent()
	if err != nil {
		log.WithError(err).Fatal("Error loading plugin config")
	}

	// Start secrets agent
	if err := secret.Add(o.hmacSecret); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	// Set up GitHub client
	ghClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	// Serve
	srv := &server{
		tokenGenerator:     secret.GetTokenGenerator(o.hmacSecret),
		gh:                 ghClient,
		log:                log,
		cfg:                conf,
		pluginsConfigAgent: pca,
	}

	mux := http.NewServeMux()
	mux.Handle("/", srv)
	externalplugins.ServeExternalPluginHelp(mux, log, getHelpProvider(conf.OrgsRepos))
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}

	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	health.ServeReady()

	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)
	log.WithField("port", o.port).Info("Serving...")
}
