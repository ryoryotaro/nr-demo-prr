package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const serviceName = "prr-demo-checkout"

var (
	commitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)
	valuePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type options struct {
	Commit    string
	DemoRunID string
	Mode      string
	DeepLink  string
	User      string
}

type client struct {
	endpoint  string
	userKey   string
	accountID int64
	http      *http.Client
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphQLResponse struct {
	Data struct {
		Create struct {
			Event *struct {
				ID     string `json:"changeTrackingId"`
				Entity struct {
					Name string `json:"name"`
					GUID string `json:"guid"`
				} `json:"entity"`
			} `json:"changeTrackingEvent"`
			Messages []string `json:"messages"`
		} `json:"changeTrackingCreateEvent"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func main() {
	opts, err := parseOptions()
	if err != nil {
		fatal(err)
	}
	c, err := newClientFromEnvironment()
	if err != nil {
		fatal(err)
	}

	result, err := c.createEvent(opts)
	if err != nil {
		fatal(err)
	}
	fmt.Println("[PASS] Deployment event recorded")
	fmt.Printf("Change Tracking ID: %s\n", result.ID)
	fmt.Printf("Entity: %s (%s)\n", result.Entity.Name, result.Entity.GUID)
	fmt.Printf("Commit: %s\n", opts.Commit)
	fmt.Printf("Version: %s\n", versionFromCommit(opts.Commit))
}

func parseOptions() (options, error) {
	var opts options
	flag.StringVar(&opts.Commit, "commit", "", "full Git commit SHA")
	flag.StringVar(&opts.DemoRunID, "demo-run-id", "", "demo run identifier")
	flag.StringVar(&opts.Mode, "mode", "", "complete or incomplete")
	flag.StringVar(&opts.DeepLink, "deep-link", "", "optional GitHub repository or pull request URL")
	flag.StringVar(&opts.User, "user", "prr-demo", "change author")
	flag.Parse()
	if flag.NArg() != 0 {
		return opts, errors.New("unexpected positional arguments")
	}
	if !commitPattern.MatchString(opts.Commit) {
		return opts, errors.New("commit must be a full 40- or 64-character hexadecimal Git SHA")
	}
	if !valuePattern.MatchString(opts.DemoRunID) {
		return opts, errors.New("demo-run-id is required and contains unsupported characters")
	}
	if opts.Mode != "complete" && opts.Mode != "incomplete" && opts.Mode != "regression" {
		return opts, errors.New("mode must be complete, incomplete, or regression")
	}
	if opts.DeepLink != "" {
		parsed, err := url.ParseRequestURI(opts.DeepLink)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return opts, errors.New("deep-link must be an absolute HTTP or HTTPS URL")
		}
	}
	if strings.TrimSpace(opts.User) == "" {
		opts.User = "prr-demo"
	}
	return opts, nil
}

func newClientFromEnvironment() (*client, error) {
	userKey := os.Getenv("NEW_RELIC_USER_KEY")
	if userKey == "" {
		return nil, errors.New("NEW_RELIC_USER_KEY is required")
	}
	accountText := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	accountID, err := strconv.ParseInt(accountText, 10, 64)
	if err != nil || accountID <= 0 {
		return nil, errors.New("NEW_RELIC_ACCOUNT_ID must be a positive integer")
	}
	endpoint := os.Getenv("NEW_RELIC_NERDGRAPH_ENDPOINT")
	if endpoint == "" {
		return nil, errors.New("NEW_RELIC_NERDGRAPH_ENDPOINT is required")
	}
	return &client{endpoint: endpoint, userKey: userKey, accountID: accountID, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

type eventResult struct {
	ID     string
	Entity struct {
		Name string
		GUID string
	}
}

func (c *client) createEvent(opts options) (eventResult, error) {
	entitySearch := fmt.Sprintf("name = '%s' AND domain = 'APM' AND accountId = %d", serviceName, c.accountID)
	deployment := map[string]any{
		"version": versionFromCommit(opts.Commit),
		"commit":  opts.Commit,
	}
	if opts.DeepLink != "" {
		deployment["deepLink"] = opts.DeepLink
	}
	event := map[string]any{
		"categoryAndTypeData": map[string]any{
			"kind":           map[string]any{"category": "Deployment", "type": "Basic"},
			"categoryFields": map[string]any{"deployment": deployment},
		},
		"entitySearch":     map[string]any{"query": entitySearch},
		"description":      "PRC demo deployment",
		"shortDescription": "PRC demo",
		"user":             opts.User,
		"customAttributes": map[string]any{"demoRunId": opts.DemoRunID, "observabilityMode": opts.Mode},
	}
	payload := graphQLRequest{
		Query: `mutation RecordChange($event: ChangeTrackingCreateEventInput!) {
  changeTrackingCreateEvent(changeTrackingEvent: $event) {
    changeTrackingEvent {
      changeTrackingId
      entity { name guid }
    }
    messages
  }
}`,
		Variables: map[string]any{"event": event},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return eventResult{}, fmt.Errorf("encode NerdGraph request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return eventResult{}, fmt.Errorf("create NerdGraph request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-Key", c.userKey)
	response, err := c.http.Do(req)
	if err != nil {
		return eventResult{}, fmt.Errorf("NerdGraph request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return eventResult{}, fmt.Errorf("read NerdGraph response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return eventResult{}, errors.New("New Relic API authentication failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return eventResult{}, fmt.Errorf("NerdGraph returned HTTP %d", response.StatusCode)
	}
	var decoded graphQLResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return eventResult{}, errors.New("decode NerdGraph response")
	}
	if len(decoded.Errors) > 0 {
		return eventResult{}, fmt.Errorf("NerdGraph error: %s", decoded.Errors[0].Message)
	}
	created := decoded.Data.Create.Event
	if created == nil || created.ID == "" {
		message := "New Relic did not return a Change Tracking event"
		if len(decoded.Data.Create.Messages) > 0 {
			message += ": " + strings.Join(decoded.Data.Create.Messages, "; ")
		}
		return eventResult{}, errors.New(message)
	}
	result := eventResult{ID: created.ID}
	result.Entity.Name = created.Entity.Name
	result.Entity.GUID = created.Entity.GUID
	return result, nil
}

func versionFromCommit(commit string) string {
	if len(commit) <= 7 {
		return commit
	}
	return commit[:7]
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
	os.Exit(2)
}
