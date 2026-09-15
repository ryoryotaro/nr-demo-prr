package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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

func TestBuildReadinessReportUsesCheckResults(t *testing.T) {
	report := buildReadinessReport(
		"prr-demo-checkout",
		"run-123",
		"NOT READY",
		[]checkResult{{Name: "tenant.id", Passed: true}, {Name: "customer.plan", Passed: false}},
		[]checkResult{{Name: "prr-demo-payment", Passed: false}},
	)

	if report.DemoRunID != "run-123" || report.Result != "NOT READY" {
		t.Fatalf("unexpected report identity: %#v", report)
	}
	want := []readinessCheck{
		{Name: "tenant.id", Status: "PASS", Evidence: "Observed for demo.run_id=run-123"},
		{Name: "customer.plan", Status: "FAIL", Evidence: "NOT OBSERVED for demo.run_id=run-123"},
		{Name: "prr-demo-payment", Status: "FAIL", Evidence: "NO shared trace.id for demo.run_id=run-123"},
	}
	if len(report.Checks) != len(want) {
		t.Fatalf("unexpected check count: %d", len(report.Checks))
	}
	for index := range want {
		if report.Checks[index] != want[index] {
			t.Fatalf("check %d: got %#v want %#v", index, report.Checks[index], want[index])
		}
	}
}

func TestWriteReadinessReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readiness.json")
	report := readinessReport{Service: "checkout", DemoRunID: "run-1", Result: "READY"}
	if err := writeReadinessReport(path, report); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded readinessReport
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, report) {
		t.Fatalf("got %#v want %#v", decoded, report)
	}
}
