package main

import (
	"flag"
	"os"

	"k8s.io/test-infra/pkg/flagutil"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
)

type options struct {
	port       int
	dryRun     bool
	github     prowflagutil.GitHubOptions
	hmacSecret string
	config     string
}

func newOptions() *options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.IntVar(&o.port, "port", 8787, "Port to listen on.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing (uses API tokens but does not mutate).")
	fs.StringVar(&o.hmacSecret, "hmac", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.config, "config", "/etc/config/config.yaml", "Path to the plugin configuration file")

	for _, group := range []flagutil.OptionGroup{&o.github} {
		group.AddFlags(fs)
	}
	fs.Parse(os.Args[1:])

	return &o
}

func (o *options) Validate() error {
	for _, group := range []flagutil.OptionGroup{&o.github} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}

	return nil
}
