package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gitpod-io/gitbot/common"

	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/pluginhelp/externalplugins"
	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"
)

const pluginName = "groundwork"

var (
	scheduleRe     = regexp.MustCompile(`(?mi)^/schedule\s*$`)
	dontScheduleRe = regexp.MustCompile(`(?mi)^/dont-schedule\s*$`)
)

type Config struct {
	OrgsRepos map[string]RepoConfig `json:"orgsRepos"`
}

type RepoConfig struct {
	Columns         ColumnConfig `json:"columns"`
	FreeForAllLimit int          `json:"freeForAllLimit"`
	TotalLimit      *int         `json:"totalLimit"`
	Scheduler       []string     `json:"scheduler,omitempty"`
}

type ColumnConfig struct {
	Inbox     *int `json:"inbox,omitempty"`
	Scheduled *int `json:"scheduled,omitempty"`
}

type options struct {
	port       int
	dryRun     bool
	github     prowflagutil.GitHubOptions
	hmacSecret string
	configPath string
}

func (o *options) Validate() error {
	for _, group := range []flagutil.OptionGroup{&o.github} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}

	return nil
}

func newOptions() *options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8787, "Port to listen on.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing (uses API tokens but does not mutate).")
	fs.StringVar(&o.hmacSecret, "hmac", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.configPath, "config", "/etc/config/config.yaml", "Path to the plugin config")

	for _, group := range []flagutil.OptionGroup{&o.github} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])

	return &o
}

func helpProvider(enabledRepos []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `Automates the groundwork project management`,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/(dont-)schedule",
		Description: "Schedules an issue in a groundwork project",
		WhoCanUse:   "Teamleads",
		Examples:    []string{"/schedule", "/dont-schedule"},
	})
	return pluginHelp, nil
}

func main() {
	o := newOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	logrus.SetFormatter(&logrus.JSONFormatter{})
	// todo(leodido) > use global option from the Prow config.
	logrus.SetLevel(logrus.DebugLevel)
	log := logrus.StandardLogger().WithField("plugin", pluginName)

	b, err := os.ReadFile(o.configPath)
	if err != nil {
		log.Fatalf("Invalid config %s: %v", o.configPath, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		log.Fatalf("error unmarshaling %s: %v", o.configPath, err)
	}
	log.WithField("config", cfg).Info("loaded config")

	secretAgent := &secret.Agent{}
	if err := secretAgent.Start([]string{o.github.TokenPath, o.hmacSecret}); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	githubClient, err := o.github.GitHubClient(secretAgent, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	serv := &server{
		tokenGenerator:       secretAgent.GetTokenGenerator(o.hmacSecret),
		githubTokenGenerator: secretAgent.GetTokenGenerator(o.github.TokenPath),
		gh:                   githubClient,
		log:                  log,
		cfg:                  cfg,
	}

	health := pjutil.NewHealth()

	log.Info("Complete serve setting")
	mux := http.NewServeMux()
	mux.Handle("/", serv)
	externalplugins.ServeExternalPluginHelp(mux, log, helpProvider)
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}

	health.ServeReady()

	defer interrupts.WaitForGracefulShutdown()
	interrupts.ListenAndServe(httpServer, 5*time.Second)
}

type server struct {
	tokenGenerator       func() []byte
	githubTokenGenerator func() []byte
	gh                   github.Client
	log                  *logrus.Entry
	cfg                  Config
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := github.ValidateWebhook(w, r, s.tokenGenerator)
	if !ok {
		return
	}
	// Event received, handle it
	if err := s.handleEvent(eventType, eventGUID, payload); err != nil {
		logrus.WithError(err).Error("Error parsing event.")
	}
}

func (s *server) handleEvent(eventType, eventGUID string, payload []byte) error {
	l := s.log.WithFields(logrus.Fields{
		"event-type":     eventType,
		github.EventGUID: eventGUID,
	})

	switch eventType {
	case "issue_comment":
		var ic github.IssueCommentEvent
		if err := json.Unmarshal(payload, &ic); err != nil {
			return err
		}
		go func() {
			if err := s.handleIssueComment(ic); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event.")
			}
		}()
	case "issues":
		var ic github.IssueEvent
		if err := json.Unmarshal(payload, &ic); err != nil {
			return err
		}
		go func() {
			if err := s.handleIssueEvent(ic); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event.")
			}
		}()
	case "pull_request":
		var pre github.PullRequestEvent
		if err := json.Unmarshal(payload, &pre); err != nil {
			return err
		}
		go func() {
			if err := s.handlePREvent(pre); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event.")
			}
		}()
	default:
		logrus.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

const (
	labelInProgress         = "groundwork: in progress"
	labelScheduled          = "groundwork: scheduled"
	labelAwaitingDeployment = "groundwork: awaiting deployment"
	labelInReview           = "groundwork: in review"

	labelPrioHighest = "priority: highest (user impact)"
	labelPrioHigh    = "priority: high (dev loop impact)"
)

func (s *server) handleIssueEvent(evt github.IssueEvent) error {
	var (
		org   = evt.Repo.Owner.Login
		repo  = evt.Repo.Name
		issue = evt.Issue.Number
	)

	switch evt.Action {
	case github.IssueActionAssigned:
		return s.replaceGroundworkLabel(org, repo, issue, labelInProgress)
	case github.IssueActionUnassigned:
		return s.replaceGroundworkLabel(org, repo, issue, labelScheduled)
	default:
		return nil
	}
}

func (s *server) handlePREvent(evt github.PullRequestEvent) error {
	switch evt.Action {
	case github.PullRequestActionClosed,
		github.PullRequestActionReadyForReview,
		github.PullRequestActionReviewRequested,
		github.PullRequestActionConvertedToDraft,
		github.PullRequestActionEdited:
		break
	default:
		return nil
	}
	var (
		org  = evt.Repo.Owner.Login
		repo = evt.Repo.Name
	)

	linkedIssues := findLinkedIssues(&evt.PullRequest)
	s.log.WithFields(logrus.Fields{"org": org, "repo": repo, "pr": evt.PullRequest.Number, "linkedIssues": linkedIssues, "action": evt.Action}).Info("handling PR event")

	var newLabel string
	if evt.Action == github.PullRequestActionClosed {
		if evt.PullRequest.Merged {
			newLabel = labelAwaitingDeployment
		} else {
			// PR was closed without being merged - remove the groundwork label
			newLabel = ""
		}
	} else if !evt.PullRequest.Draft && len(evt.PullRequest.RequestedReviewers) > 0 {
		newLabel = labelInReview
	} else {
		newLabel = labelInProgress
	}

	for _, issue := range linkedIssues {
		err := s.replaceGroundworkLabel(org, repo, issue, newLabel)
		if err != nil {
			s.log.WithError(err).WithField("issue", issue).Infof("cannot add \"%s\" label", newLabel)
		}
	}

	return nil
}

func (s *server) replaceGroundworkLabel(org, repo string, issue int, newLabel string) error {
	lbls, err := s.gh.GetIssueLabels(org, repo, issue)
	if err != nil {
		return err
	}

	var groundworkLabels []string
	for _, lbl := range lbls {
		if strings.HasPrefix(lbl.Name, "groundwork: ") {
			groundworkLabels = append(groundworkLabels, lbl.Name)
		}
	}
	if len(groundworkLabels) == 0 {
		return nil
	}

	for _, lbl := range groundworkLabels {
		if lbl == newLabel {
			// we're just re-adding the same label, do nothing
			return nil
		}

		err := s.gh.RemoveLabel(org, repo, issue, lbl)
		if err != nil {
			s.log.WithError(err).WithField("issue", issue).WithField("label", lbl).Warn("cannot remove label")
		}
	}

	if newLabel != "" {
		err = s.gh.AddLabel(org, repo, issue, newLabel)
		if err != nil {
			return err
		}
	}

	return nil
}

var fixesRe = regexp.MustCompile(`(?mi)^[fF]ixes (#|(https:\/\/github.com\/[\w-]+\/[\w-]+\/issues\/))(?P<issue>\d+)\s*$`)

func findLinkedIssues(pr *github.PullRequest) []int {
	// It would seem that there's no way to get the list of linked issues from GitHub.
	// Boooooo.

	matches := fixesRe.FindAllStringSubmatch(pr.Body, -1)
	res := make([]int, len(matches))
	for i, issue := range matches {
		res[i], _ = strconv.Atoi(issue[3])
	}
	return res
}

func (s *server) handleIssueComment(ic github.IssueCommentEvent) error {
	l := s.log.WithFields(logrus.Fields{
		"body": ic.Comment.Body,
	})
	if ic.Action != github.IssueCommentActionCreated || ic.Issue.State == "closed" {
		return nil
	}

	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	num := ic.Issue.Number

	repocfg, ok := s.cfg.OrgsRepos[fmt.Sprintf("%s/%s", org, repo)]
	if !ok {
		return nil
	}

	var (
		schedule     = scheduleRe.MatchString(ic.Comment.Body)
		dontSchedule = dontScheduleRe.MatchString(ic.Comment.Body)
	)
	if !schedule && !dontSchedule {
		return nil
	}

	scheduledCards, err := s.gh.GetColumnProjectCards(org, *repocfg.Columns.Scheduled)
	if err != nil {
		return err
	}
	cardCount := len(scheduledCards)

	author := ic.Comment.User.Login
	var permitted bool
	if cardCount < repocfg.FreeForAllLimit {
		permitted = true
	} else {
		for _, u := range repocfg.Scheduler {
			if u == author {
				permitted = true
				break
			}
		}
	}
	if !permitted {
		var scheduler string
		for _, u := range repocfg.Scheduler {
			scheduler += fmt.Sprintf("  - %s\n", u)
		}
		l.WithFields(logrus.Fields{"author": author}).Info("rejecting scheduling request")
		return s.gh.CreateComment(org, repo, num, plugins.FormatICResponse(ic.Comment, fmt.Sprintf("We have more than %d cards scheduled. Above %d only scheduler can perform this action. Those are:\n"+scheduler, cardCount, repocfg.FreeForAllLimit)))
	}

	if repocfg.TotalLimit != nil && cardCount > *repocfg.TotalLimit {
		return s.gh.CreateComment(org, repo, num, plugins.FormatICResponse(ic.Comment, fmt.Sprintf("Cannot schedule issue - scheduled items limit (%d) has been reached.", *repocfg.TotalLimit)))
	}

	if schedule {
		err := s.gh.AddLabel(org, repo, num, labelScheduled)
		if err != nil {
			return err
		}

		cardID, err := common.FindCardByIssueURL(s.gh, ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, *repocfg.Columns.Inbox)
		if err != nil {
			return err
		}

		if cardID == nil {
			// card is not part of the project yet
			card, err := s.gh.CreateProjectCard(org, *repocfg.Columns.Scheduled, github.ProjectCard{
				ContentType: "Issue",
				ContentID:   ic.Issue.ID,
			})
			if err == nil {
				cardID = &card.ID
			} else {
				logrus.WithError(err).Warn("card creation failed - will try to move irregardless")

				// bug in test-infra Github client: card creation succeeds, but error and no response is returned
				cardID, err = common.FindCardByIssueURL(s.gh, ic.Repo.Owner.Login, ic.Repo.Name, ic.Issue.Number, *repocfg.Columns.Inbox)
				if err != nil {
					return err
				}
			}
		}

		if cardID == nil {
			logrus.WithFields(logrus.Fields{"issue": ic.Issue.Number, "org": ic.Repo.Owner.Login, "repo": ic.Repo.Name}).Warn("did not move card to scheduled column, but should have")
			return nil
		}

		var position = "bottom"
		for _, l := range ic.Issue.Labels {
			if l.Name == labelPrioHigh || l.Name == labelPrioHighest {
				position = "top"
				break
			}
		}

		return common.MoveProjectCard(s.githubTokenGenerator, org, *cardID, *repocfg.Columns.Scheduled, position)
	} else if dontSchedule {
		return s.gh.CreateComment(org, repo, num, plugins.FormatICResponse(ic.Comment, "dont-schedule is not yet supported"))
	}

	return nil
}
