package main

import (
	"fmt"
	"flag"
	"time"

	"github.com/sirupsen/logrus"

	pluginsflagutil "k8s.io/test-infra/prow/flagutil/plugins"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
)

type options struct {
	port          int
	dryRun        bool
	hmacSecret    string
	configPath    string
	updatePeriod  time.Duration
	github        prowflagutil.GitHubOptions
}

func (o *options) Validate() error {
	// TODO(arthursens): actually validate options
	return nil
}

func newOptions() options {
	o := options{}

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.IntVar(&o.port, "port", 8888, "Port to listen on.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.StringVar(&o.hmacSecret, "hmac", "/etc/webhook/hmac", "Path to the file containing the Github HMAC secret.")
	fs.StringVar(&o.configPath, "config", "/etc/config/config.yaml", "Path to the plugin config")
	fs.DurationVar(&o.updatePeriod, "update-period", time.Hour*168, "Period duration for periodic scans of all open post-mortem issues.")

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
}