package gitbot

import (
	"net/http"
	"os"

	"github.com/csweichel/gitbot/bot"
	"gopkg.in/yaml.v3"
)

func HandleGHWebhook(w http.ResponseWriter, r *http.Request) {
	fd, err := os.Open("./serverless_function_source_code/config/config.yaml")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dec := yaml.NewDecoder(fd)
	dec.KnownFields(true)
	var cfg bot.Config
	err = dec.Decode(&cfg)
	fd.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	b, err := bot.New(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	b.HandleGithubWebhook(w, r)
}
