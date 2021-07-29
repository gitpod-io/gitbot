package main

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

type Config struct {
	OrgsRepos map[string]RepoConfig `json:"orgsRepos" yaml:"orgsRepos"`
}

type RepoConfig []LabelConfig

type LabelConfig struct {
	Key    string   `json:"key" yaml:"key"`
	Values []string `json:"values" yaml:"values"`
}

func (c Config) getLabels() map[string]sets.String {
	res := make(map[string]sets.String, len(c.OrgsRepos))
	for repo, lbls := range c.OrgsRepos {
		res[repo] = lbls.getLabels()
	}

	return res
}

func (c Config) getMatchers() map[string]matchers {
	res := make(map[string]matchers, len(c.OrgsRepos))
	for repo, lbls := range c.OrgsRepos {
		res[repo] = matchers{
			lbls.getAddRegex(),
			lbls.getDelRegex(),
		}
	}

	return res

}

func (rc RepoConfig) getLabelKeys() []string {
	res := []string{}
	for _, elem := range rc {
		res = append(res, elem.Key)
	}

	return res
}

func (rc RepoConfig) getExamples() []string {
	res := []string{}
	for _, elem := range rc {
		idx1 := rand.Intn(len(elem.Values))
		idx2 := rand.Intn(len(elem.Values))
		pick1 := elem.Values[idx1]
		pick2 := elem.Values[idx2]
		res = append(res, fmt.Sprintf("/%s %q", elem.Key, pick1), fmt.Sprintf("/remove-%s %q", elem.Key, pick2))
	}

	return res
}

func (rc RepoConfig) getLabels() sets.String {
	res := sets.NewString()
	for _, elem := range rc {
		for _, value := range elem.Values {
			res.Insert(fmt.Sprintf("%s: %s", elem.Key, value))
		}
	}

	return res
}

func (rc RepoConfig) getAddRegex() *regexp.Regexp {
	re := fmt.Sprintf(`(?m)^/(%s)\s*(.*?)\s*$`, strings.Join(rc.getLabelKeys(), "|"))
	return regexp.MustCompile(re)
}

func (rc RepoConfig) getDelRegex() *regexp.Regexp {
	re := fmt.Sprintf(`(?m)^/remove-(%s)\s*(.*?)\s*$`, strings.Join(rc.getLabelKeys(), "|"))
	return regexp.MustCompile(re)
}
