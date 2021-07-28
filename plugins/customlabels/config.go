package main

import (
	"fmt"
	"math/rand"
)

type Config struct {
	OrgsRepos map[string]RepoConfig `json:"orgsRepos" yaml:"orgsRepos"`
}

type RepoConfig []LabelConfig

type LabelConfig struct {
	Key    string   `json:"key" yaml:"key"`
	Values []string `json:"values" yaml:"values"`
}

func (rc RepoConfig) getLabelKeys() []string {
	res := []string{}
	for _, item := range rc {
		res = append(res, item.Key)
	}

	return res
}

func (rc RepoConfig) getExamples() []string {
	res := []string{}
	for _, item := range rc {
		idx1 := rand.Intn(len(item.Values))
		idx2 := rand.Intn(len(item.Values))
		pick1 := item.Values[idx1]
		pick2 := item.Values[idx2]
		res = append(res, fmt.Sprintf("/%s %q", item.Key, pick1), fmt.Sprintf("/remove-%s %q", item.Key, pick2))
	}

	return res
}
