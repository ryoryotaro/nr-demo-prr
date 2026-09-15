package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestVersionFromCommit(t *testing.T) {
	commit := "abcdef1234567890abcdef1234567890abcdef12"
	if got := versionFromCommit(commit); got != "abcdef1" {
		t.Fatalf("versionFromCommit() = %q", got)
	}
}

func TestCreateEvent(t *testing.T) {
	var request graphQLRequest
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("API-Key"); got != "secret" {
			t.Errorf("API-Key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"data":{"changeTrackingCreateEvent":{"changeTrackingEvent":{"changeTrackingId":"change-1","entity":{"name":"prr-demo-checkout","guid":"guid-1"}},"messages":[]}}}`)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}

	c := &client{endpoint: "https://example.invalid/graphql", userKey: "secret", accountID: 12345, http: httpClient}
	commit := "abcdef1234567890abcdef1234567890abcdef12"
	result, err := c.createEvent(options{Commit: commit, DemoRunID: "run-1", Mode: "incomplete", DeepLink: "https://github.com/example/repo/pull/1", User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "change-1" {
		t.Fatalf("ID = %q", result.ID)
	}
	event := request.Variables["event"].(map[string]any)
	search := event["entitySearch"].(map[string]any)["query"].(string)
	if search != "name = 'prr-demo-checkout' AND domain = 'APM' AND accountId = 12345" {
		t.Fatalf("entity search = %q", search)
	}
	encoded, _ := json.Marshal(event)
	for _, want := range []string{commit, `"demoRunId":"run-1"`, `"observabilityMode":"incomplete"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("event does not contain %q: %s", want, encoded)
		}
	}
}
