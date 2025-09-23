package main

import (
	"time"
	"errors"
	"net/http"
	"net/url"
	"io"
	"strings"
	"strconv"
	"encoding/base64"
	"log"
)

type zdUserIdentities struct {
	Identities []zdUserIdentity `json:"identities"`
}

type zdUserIdentity struct {
	ID                 int64     `json:"id"`
	URL                string    `json:"url"`
	Type               string    `json:"type"`
	Value              string    `json:"value,omitempty"`
	Primary            bool      `json:"primary"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	UndeliverableCount int       `json:"undeliverable_count,omitempty"`
	DeliverableState   string    `json:"deliverable_state,omitempty"`
}

func zdMergeIdIntoId(removeId, keepId int64) error {
	if removeId == keepId {
		return nil
	}

	apiURL := "https://" + conf.GetString("ZD_DOMAIN") + ".zendesk.com/api/v2/users/" + strconv.FormatInt(removeId, 10) + "/merge"
	reqURL, err := url.Parse(apiURL)
	if err != nil {
		return err
	}
	reqBody := io.NopCloser(strings.NewReader(`{"user": {"id": ` + strconv.FormatInt(keepId, 10) + `} }`))
	log.Printf("-> %s\n", conf.GetString("ZD_API_USER") + "/token:" + conf.GetString("ZD_API_TOKEN"))
	reqCreds := base64.StdEncoding.EncodeToString([]byte(conf.GetString("ZD_API_USER") + "/token:" + conf.GetString("ZD_API_TOKEN")))
	req := http.Request{
		Method: http.MethodPut,
		URL: reqURL,
		Body: reqBody,
	}
	req.Header = make(map[string][]string)
	req.Header.Set("Authorization", "Basic " + reqCreds)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(&req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}

	return nil
}