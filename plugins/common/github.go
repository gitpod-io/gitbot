package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"k8s.io/test-infra/prow/github"
)

func FindCardByIssueURL(gh github.Client, org, repo string, number int, col int) (cardID *int, err error) {
	issueURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%v", org, repo, number)

	inboxCards, err := gh.GetColumnProjectCards(org, col)
	if err != nil {
		return
	}
	for _, c := range inboxCards {
		if c.ContentURL == issueURL {
			cardID = &c.ID
			break
		}
	}
	return
}

func MoveProjectCard(tokenGenerator func() []byte, org string, cardID, columnID int, position string) error {
	reqParams := struct {
		Position string `json:"position"`
		ColumnID int    `json:"column_id"`
	}{position, columnID}
	body, err := json.Marshal(reqParams)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("https://api.github.com/projects/columns/cards/%d/moves", cardID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader(tokenGenerator))
	req.Header.Set("Accept", "application/vnd.github.inertia-preview+json")
	// Disable keep-alive so that we don't get flakes when GitHub closes the
	// connection prematurely.
	// https://go-review.googlesource.com/#/c/3210/ fixed it for GET, but not
	// for POST.
	req.Close = true

	client := http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf("moveProjectCard: non-20x GitHub response (%d): %s", resp.StatusCode, string(b))
	}

	return nil
}

func authHeader(tokenSource func() []byte) string {
	if tokenSource == nil {
		return ""
	}
	token := tokenSource()
	if len(token) == 0 {
		return ""
	}
	return fmt.Sprintf("Bearer %s", token)
}
