package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
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

const pluginName = "customlabels"

func configString(labelCommands []string) string {
	var formatted []string
	for _, labelCommand := range labelCommands {
		formatted = append(formatted, fmt.Sprintf(`"%s/*"`, labelCommand))
	}
	return fmt.Sprintf("The custom labels plugin works on %s and %s labels.", strings.Join(formatted[:len(formatted)-1], ", "), formatted[len(formatted)-1])
}

func getHelpProvider(labelsPerOrgRepo map[string]RepoConfig) externalplugins.ExternalPluginHelpProvider {
	return func(enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
		pluginHelp := &pluginhelp.PluginHelp{
			Description: `The customlabels plugin provides commands that add or remove certain GitHub labels. Labels are configurable.`,
		}

		cfg := make(map[string]string, len(enabledRepos))
		var keysApplyingToAll []string
		var allConfig RepoConfig
		oneAppliesToAllRepos := false
		for _, orgrepo := range enabledRepos {
			k := ""
			if orgrepo.Org != "" && orgrepo.Repo != "" {
				k = orgrepo.String()
			}

			rc, ok := labelsPerOrgRepo[k]
			if !ok {
				logrus.WithField("repo", k).Error("No custom labels found")
				return nil, fmt.Errorf("Error")
			}
			keys := rc.getLabelKeys()
			if len(keys) == 0 {
				logrus.WithField("repo", k).Error("No custom labels keys found")
			}

			cfg[k] = configString(keys)

			if k == "" {
				oneAppliesToAllRepos = true
				keysApplyingToAll = keys
				allConfig = rc
			} else {
				pluginHelp.AddCommand(pluginhelp.Command{
					Usage:       fmt.Sprintf("/[remove-](%s) <target>", strings.Join(keys, "|")),
					Description: fmt.Sprintf("Applies or removes a label from one of the recognized types to repo %q.", k),
					Featured:    true,
					Examples:    rc.getExamples(),
					WhoCanUse:   "Anyone",
				})
			}
		}

		pluginHelp.Config = cfg

		if oneAppliesToAllRepos {
			pluginHelp.AddCommand(pluginhelp.Command{
				Usage:       fmt.Sprintf("/[remove-](%s) <target>", strings.Join(keysApplyingToAll, "|")),
				Description: "Applies or removes a label from one of the recognized types to all the repositories",
				Featured:    true,
				Examples:    allConfig.getExamples(),
				WhoCanUse:   "Anyone",
			})
		}

		return pluginHelp, nil
	}
}

func main() {
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

	matchers := conf.getMatchers()
	supported := conf.getLabels()
	if len(matchers) == 0 || len(supported) == 0 {
		log.Fatalf("Missing matchers or/and definition of supported labels")
	}
	log.WithField("matchers", matchers).Info("Matchers loaded")
	log.WithField("labels", supported).Info("Supported labels")

	// Start secrets agent
	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.github.TokenPath, o.hmacSecret}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	// Set up GitHub client
	ghClient, err := o.github.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	// Serve
	srv := &server{
		tokenGenerator:       secretAgent.GetTokenGenerator(o.hmacSecret),
		githubTokenGenerator: secretAgent.GetTokenGenerator(o.github.TokenPath),
		gh:                   ghClient,
		log:                  log,
		cfg:                  conf,
		repoMatchers:         matchers,
		repoValidLabels:      supported,
	}

	health := pjutil.NewHealth()

	mux := http.NewServeMux()
	mux.Handle("/", srv)
	externalplugins.ServeExternalPluginHelp(mux, log, getHelpProvider(conf.OrgsRepos))
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}

	health.ServeReady()

	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)
	log.WithField("port", o.port).Info("Serving...")
}
