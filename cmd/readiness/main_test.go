package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadContract(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "contract.yaml")
	contents := "service: checkout\nrequired_attributes:\n  - tenant.id\nrequired_dependencies:\n  - payment\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	contract, err := loadContract(path)
	if err != nil {
		t.Fatal(err)
	}
	if contract.Service != "checkout" || contract.RequiredAttributes[0] != "tenant.id" || contract.RequiredDependencies[0] != "payment" {
		t.Fatalf("unexpected contract: %#v", contract)
	}
}

func TestNerdGraphChecks(t *testing.T) {
	const userKey = "secret-user-key"
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("API-Key") != userKey {
			t.Error("API-Key header was not set")
		}
		var request graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(request.Query, userKey) {
			t.Error("user key must not be included in the GraphQL body")
		}
		nrql, _ := request.Variables["nrql"].(string)
		responseBody := `{"data":{"actor":{"account":{"nrql":{"results":[{"facet":"trace-id","apps":["prr-demo-checkout","prr-demo-payment"]}]}}}}}`
		if strings.Contains(nrql, "FROM Transaction") {
			responseBody = `{"data":{"actor":{"account":{"nrql":{"results":[{"presentCount":1}]}}}}}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Header:     make(http.Header),
		}, nil
	})

	client := &nerdGraphClient{
		endpoint:  "https://example.invalid/graphql",
		userKey:   userKey,
		accountID: 123,
		http:      &http.Client{Timeout: time.Second, Transport: transport},
	}

	attributePassed, err := client.attributeExists("prr-demo-checkout", "tenant.id", "run-123")
	if err != nil || !attributePassed {
		t.Fatalf("attribute check failed: passed=%v err=%v", attributePassed, err)
	}
	dependencyPassed, err := client.dependencyExists("prr-demo-checkout", "prr-demo-payment", "run-123")
	if err != nil || !dependencyPassed {
		t.Fatalf("dependency check failed: passed=%v err=%v", dependencyPassed, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestLoadContractRejectsUnsupportedYAML(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "contract.yaml")
	if err := os.WriteFile(path, []byte("service: checkout\nunknown: value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadContract(path); err == nil {
		t.Fatal("expected unsupported YAML to fail")
	}
}
