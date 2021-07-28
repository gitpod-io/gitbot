package main

import (
	"net/http"

	"github.com/sirupsen/logrus"
	prowgithub "k8s.io/test-infra/prow/github"
)

type server struct {
	tokenGenerator       func() []byte
	githubTokenGenerator func() []byte
	gh                   prowgithub.Client
	log                  *logrus.Entry
	cfg                  Config
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {

}

func (s *server) handleEvent(eventType, eventGUID string, payload []byte) error {
	return nil
}
