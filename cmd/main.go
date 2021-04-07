package main

import (
	"fmt"
	"os"

	"github.com/csweichel/gitbot/bot"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"gopkg.in/yaml.v3"
)

func main() {
	app := &cli.App{
		Name:  "gitbot",
		Usage: "gitbot is a bot for gitpod's development",
		Commands: []cli.Command{
			{
				Name:  "run",
				Usage: "runs gitbot",
				Flags: []cli.Flag{
					cli.StringFlag{
						Name:  "config",
						Usage: "path to the config file",
					},
				},
				Action: func(c *cli.Context) error {
					return run(c.String("config"))
				},
			},
			{
				Name:  "init",
				Usage: "dumps an example config to stdout",
				Action: func(c *cli.Context) error {
					return yaml.NewEncoder(os.Stdout).Encode(bot.Config{})
				},
			},
		},
	}
	err := app.Run(os.Args)
	if err != nil {
		logrus.WithError(err).Fatal()
	}
}

func run(configFN string) error {
	fd, err := os.Open(configFN)
	if err != nil {
		return fmt.Errorf("cannot open config file %v: %w", configFN, err)
	}
	defer fd.Close()

	dec := yaml.NewDecoder(fd)
	dec.KnownFields(true)
	var cfg bot.Config
	err = dec.Decode(&cfg)
	if err != nil {
		return fmt.Errorf("cannot load config: %w", err)
	}

	b, err := bot.New(cfg)
	if err != nil {
		return err
	}

	return b.Run()
}
