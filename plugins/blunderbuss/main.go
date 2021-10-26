/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package blunderbuss

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pkg/layeredsets"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/pluginhelp/externalplugins"
	"k8s.io/test-infra/prow/repoowners"
)

const (
	// PluginName defines this plugin's registered name.
	PluginName = "blunderbuss"
)

var (
	match = regexp.MustCompile(`(?mi)^/auto-cc\s*$`)
	ccRegexp = regexp.MustCompile(`(?mi)^/(un)?cc(( +@?[-/\w]+?)*)\s*$`)
)

func configString(reviewCount int) string {
	var pluralSuffix string
	if reviewCount > 1 {
		pluralSuffix = "s"
	}
	return fmt.Sprintf("Blunderbuss is currently configured to request reviews from %d reviewer%s.", reviewCount, pluralSuffix)
}

func helpProvider([]config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The blunderbuss plugin automatically requests reviews from reviewers when a new PR is created. The reviewers are selected based on the reviewers specified in the OWNERS files that apply to the files modified by the PR.",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/auto-cc",
		Featured:    false,
		Description: "Manually request reviews from reviewers for a PR. Useful if OWNERS file were updated since the PR was opened.",
		Examples:    []string{"/auto-cc"},
		WhoCanUse:   "Anyone",
	})
	return pluginHelp, nil
}

type reviewersClient interface {
	FindReviewersOwnersForFile(path string) string
	Reviewers(path string) layeredsets.String
	RequiredReviewers(path string) sets.String
	LeafReviewers(path string) sets.String
}

type ownersClient interface {
	reviewersClient
	FindApproverOwnersForFile(path string) string
	Approvers(path string) layeredsets.String
	LeafApprovers(path string) sets.String
}

type fallbackReviewersClient struct {
	ownersClient
}

func (foc fallbackReviewersClient) FindReviewersOwnersForFile(path string) string {
	return foc.ownersClient.FindApproverOwnersForFile(path)
}

func (foc fallbackReviewersClient) Reviewers(path string) layeredsets.String {
	return foc.ownersClient.Approvers(path)
}

func (foc fallbackReviewersClient) LeafReviewers(path string) sets.String {
	return foc.ownersClient.LeafApprovers(path)
}

type githubClient interface {
	RequestReview(org, repo string, number int, logins []string) error
	GetPullRequestChanges(org, repo string, number int) ([]github.PullRequestChange, error)
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	Query(context.Context, interface{}, map[string]interface{}) error
}

type repoownersClient interface {
	LoadRepoOwners(org, repo, base string) (repoowners.RepoOwner, error)
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
	fs.StringVar(&o.configPath, "config", "/etc/blunderbuss", "Path to the blunderbuss config")

	for _, group := range []flagutil.OptionGroup{&o.github} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])

	return &o
}

func main() {
	o := newOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	logrus.SetFormatter(&logrus.JSONFormatter{})
	// todo(leodido) > use global option from the Prow config.
	logrus.SetLevel(logrus.DebugLevel)
	log := logrus.StandardLogger().WithField("plugin", PluginName)

	b, err := os.ReadFile(o.configPath)
	if err != nil {
		log.Fatalf("Invalid config %s: %v", o.configPath, err)
	}
	var cfg = &Blunderbuss{}
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
		eventTokenGenerator:  secretAgent.GetTokenGenerator(o.hmacSecret),
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
	eventTokenGenerator  func() []byte
	githubTokenGenerator func() []byte
	gh                   github.Client
	log                  *logrus.Entry
	cfg                  *Blunderbuss
}

type Blunderbuss struct {
	ReviewerCount *int `json:"request_count,omitempty"`
	MaxReviewerCount int `json:"max_request_count,omitempty"`
	ExcludeApprovers bool `json:"exclude_approvers,omitempty"`
	UseStatusAvailability bool `json:"use_status_availability,omitempty"`
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := github.ValidateWebhook(w, r, s.eventTokenGenerator)
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
	case "pull-request":
		var pre github.PullRequestEvent
		if err := json.Unmarshal(payload, &pre); err != nil {
			return err
		}
		go func() {
			if err := s.handlePullRequestEvent(pre); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event.")
			}
		}()
	case "pull_request_review", "pull_request_review_comment":
		var ic github.GenericCommentEvent
		if err := json.Unmarshal(payload, &ic); err != nil {
			return err
		}
		go func() {
			if err := s.handleGenericCommentEvent(ic); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("Error handling event.")
			}
		}()
	default:
		logrus.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

func (s *server) handlePullRequestEvent(/* pc plugins.Agent, */pre github.PullRequestEvent) error {
	return handlePullRequest(
		s.gh,
		pc.OwnersClient,
		s.log,
		*s.cfg,
		pre.Action,
		&pre.PullRequest,
		&pre.Repo,
	)
}

func handlePullRequest(ghc githubClient, roc repoownersClient, log *logrus.Entry, config Blunderbuss, action github.PullRequestEventAction, pr *github.PullRequest, repo *github.Repo) error {
	if (action != github.PullRequestActionOpened && action != github.PullRequestActionReadyForReview) || ccRegexp.MatchString(pr.Body) {
		return nil
	}

	return handle(
		ghc,
		roc,
		log,
		config.ReviewerCount,
		config.MaxReviewerCount,
		config.ExcludeApprovers,
		config.UseStatusAvailability,
		repo,
		pr,
	)
}

func (s *server) handleGenericCommentEvent(/* pc plugins.Agent, */ce github.GenericCommentEvent) error {
	return handleGenericComment(
		s.gh,
		pc.OwnersClient,
		s.log,
		*s.cfg,
		ce.Action,
		ce.IsPR,
		ce.Number,
		ce.IssueState,
		&ce.Repo,
		ce.Body,
	)
}

func handleGenericComment(ghc githubClient, roc repoownersClient, log *logrus.Entry, config Blunderbuss, action github.GenericCommentEventAction, isPR bool, prNumber int, issueState string, repo *github.Repo, body string) error {
	if action != github.GenericCommentActionCreated || !isPR || issueState == "closed" {
		return nil
	}

	if !match.MatchString(body) {
		return nil
	}

	pr, err := ghc.GetPullRequest(repo.Owner.Login, repo.Name, prNumber)
	if err != nil {
		return fmt.Errorf("error loading PullRequest: %w", err)
	}

	return handle(
		ghc,
		roc,
		log,
		config.ReviewerCount,
		config.MaxReviewerCount,
		config.ExcludeApprovers,
		config.UseStatusAvailability,
		repo,
		pr,
	)
}

func handle(ghc githubClient, roc repoownersClient, log *logrus.Entry, reviewerCount *int, maxReviewers int, excludeApprovers bool, useStatusAvailability bool, repo *github.Repo, pr *github.PullRequest) error {
	if pr.Draft {
		log.Infof("Skipping draft PR #%d", pr.Number)
		return nil
	}
	oc, err := roc.LoadRepoOwners(repo.Owner.Login, repo.Name, pr.Base.Ref)
	if err != nil {
		return fmt.Errorf("error loading RepoOwners: %w", err)
	}

	changes, err := ghc.GetPullRequestChanges(repo.Owner.Login, repo.Name, pr.Number)
	if err != nil {
		return fmt.Errorf("error getting PR changes: %w", err)
	}

	var reviewers []string
	var requiredReviewers []string
	if reviewerCount != nil {
		reviewers, requiredReviewers, err = getReviewers(oc, ghc, log, pr.User.Login, changes, *reviewerCount, useStatusAvailability)
		if err != nil {
			return err
		}
		if missing := *reviewerCount - len(reviewers); missing > 0 {
			if !excludeApprovers {
				// Attempt to use approvers as additional reviewers. This must use
				// reviewerCount instead of missing because owners can be both reviewers
				// and approvers and the search might stop too early if it finds
				// duplicates.
				frc := fallbackReviewersClient{ownersClient: oc}
				approvers, _, err := getReviewers(frc, ghc, log, pr.User.Login, changes, *reviewerCount, useStatusAvailability)
				if err != nil {
					return err
				}
				var added int
				combinedReviewers := sets.NewString(reviewers...)
				for _, approver := range approvers {
					if !combinedReviewers.Has(approver) {
						reviewers = append(reviewers, approver)
						combinedReviewers.Insert(approver)
						added++
					}
				}
				log.Infof("Added %d approvers as reviewers. %d/%d reviewers found.", added, combinedReviewers.Len(), *reviewerCount)
			}
		}
		if missing := *reviewerCount - len(reviewers); missing > 0 {
			log.Debugf("Not enough reviewers found in OWNERS files for files touched by this PR. %d/%d reviewers found.", len(reviewers), *reviewerCount)
		}
	}

	if maxReviewers > 0 && len(reviewers) > maxReviewers {
		log.Infof("Limiting request of %d reviewers to %d maxReviewers.", len(reviewers), maxReviewers)
		reviewers = reviewers[:maxReviewers]
	}

	// add required reviewers if any
	reviewers = append(reviewers, requiredReviewers...)

	if len(reviewers) > 0 {
		log.Infof("Requesting reviews from users %s.", reviewers)
		return ghc.RequestReview(repo.Owner.Login, repo.Name, pr.Number, reviewers)
	}
	return nil
}

func getReviewers(rc reviewersClient, ghc githubClient, log *logrus.Entry, author string, files []github.PullRequestChange, minReviewers int, useStatusAvailability bool) ([]string, []string, error) {
	authorSet := sets.NewString(github.NormLogin(author))
	reviewers := layeredsets.NewString()
	requiredReviewers := sets.NewString()
	leafReviewers := layeredsets.NewString()
	busyReviewers := sets.NewString()
	ownersSeen := sets.NewString()
	if minReviewers == 0 {
		return reviewers.List(), requiredReviewers.List(), nil
	}
	// first build 'reviewers' by taking a unique reviewer from each OWNERS file.
	for _, file := range files {
		ownersFile := rc.FindReviewersOwnersForFile(file.Filename)
		if ownersSeen.Has(ownersFile) {
			continue
		}
		ownersSeen.Insert(ownersFile)

		// record required reviewers if any
		requiredReviewers.Insert(rc.RequiredReviewers(file.Filename).UnsortedList()...)

		fileUnusedLeafs := layeredsets.NewString(rc.LeafReviewers(file.Filename).List()...).Difference(reviewers.Set()).Difference(authorSet)
		if fileUnusedLeafs.Len() == 0 {
			continue
		}
		leafReviewers = leafReviewers.Union(fileUnusedLeafs)
		if r := findReviewer(ghc, log, useStatusAvailability, &busyReviewers, &fileUnusedLeafs); r != "" {
			reviewers.Insert(0, r)
		}
	}
	// now ensure that we request review from at least minReviewers reviewers. Favor leaf reviewers.
	unusedLeafs := leafReviewers.Difference(reviewers.Set())
	for reviewers.Len() < minReviewers && unusedLeafs.Len() > 0 {
		if r := findReviewer(ghc, log, useStatusAvailability, &busyReviewers, &unusedLeafs); r != "" {
			reviewers.Insert(1, r)
		}
	}
	for _, file := range files {
		if reviewers.Len() >= minReviewers {
			break
		}
		fileReviewers := rc.Reviewers(file.Filename).Difference(authorSet)
		for reviewers.Len() < minReviewers && fileReviewers.Len() > 0 {
			if r := findReviewer(ghc, log, useStatusAvailability, &busyReviewers, &fileReviewers); r != "" {
				reviewers.Insert(2, r)
			}
		}
	}
	return reviewers.List(), requiredReviewers.List(), nil
}

// findReviewer finds a reviewer from a set, potentially using status
// availability.
func findReviewer(ghc githubClient, log *logrus.Entry, useStatusAvailability bool, busyReviewers *sets.String, targetSet *layeredsets.String) string {
	// if we don't care about status availability, just pop a target from the set
	if !useStatusAvailability {
		return targetSet.PopRandom()
	}

	// if we do care, start looping through the candidates
	for {
		if targetSet.Len() == 0 {
			// if there are no candidates left, then break
			break
		}
		candidate := targetSet.PopRandom()
		if busyReviewers.Has(candidate) {
			// we've already verified this reviewer is busy
			continue
		}
		busy, err := isUserBusy(ghc, candidate)
		if err != nil {
			log.Errorf("error checking user availability: %v", err)
		}
		if !busy {
			return candidate
		}
		// if we haven't returned the candidate, then they must be busy.
		busyReviewers.Insert(candidate)
	}
	return ""
}

type githubAvailabilityQuery struct {
	User struct {
		Login  githubql.String
		Status struct {
			IndicatesLimitedAvailability githubql.Boolean
		}
	} `graphql:"user(login: $user)"`
}

func isUserBusy(ghc githubClient, user string) (bool, error) {
	var query githubAvailabilityQuery
	vars := map[string]interface{}{
		"user": githubql.String(user),
	}
	ctx := context.Background()
	err := ghc.Query(ctx, &query, vars)
	return bool(query.User.Status.IndicatesLimitedAvailability), err
}
