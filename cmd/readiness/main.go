package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const window = "30 minutes"

var contractValuePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type contract struct {
	Service              string
	RequiredAttributes   []string
	RequiredDependencies []string
}

type nerdGraphClient struct {
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
		Actor struct {
			Account *struct {
				NRQL struct {
					Results []map[string]any `json:"results"`
				} `json:"nrql"`
			} `json:"account"`
		} `json:"actor"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type checkResult struct {
	Name   string
	Passed bool
	Err    error
}

func main() {
	contractPath := "observability-contract.yaml"
	if len(os.Args) == 2 {
		contractPath = os.Args[1]
	} else if len(os.Args) > 2 {
		printErrorAndExit("usage: readiness [contract path]")
	}

	c, err := loadContract(contractPath)
	if err != nil {
		printErrorAndExit(err.Error())
	}
	demoRunID := os.Getenv("DEMO_RUN_ID")
	if demoRunID == "" || !contractValuePattern.MatchString(demoRunID) {
		printHeader(c, demoRunID)
		fmt.Println("[ERROR] DEMO_RUN_ID is required and must contain only letters, numbers, dot, underscore, or hyphen")
		fmt.Println("\nRESULT: ERROR")
		os.Exit(2)
	}
	client, err := newNerdGraphClientFromEnvironment()
	if err != nil {
		printHeader(c, demoRunID)
		fmt.Printf("[ERROR] %v\n\nRESULT: ERROR\n", err)
		os.Exit(2)
	}

	attributeResults := make([]checkResult, 0, len(c.RequiredAttributes))
	dependencyResults := make([]checkResult, 0, len(c.RequiredDependencies))
	for _, attribute := range c.RequiredAttributes {
		passed, queryErr := client.attributeExists(c.Service, attribute, demoRunID)
		attributeResults = append(attributeResults, checkResult{Name: attribute, Passed: passed, Err: queryErr})
	}
	for _, dependency := range c.RequiredDependencies {
		passed, queryErr := client.dependencyExists(c.Service, dependency, demoRunID)
		dependencyResults = append(dependencyResults, checkResult{Name: dependency, Passed: passed, Err: queryErr})
	}

	printHeader(c, demoRunID)
	hasFailure, hasError := printResults("Required attributes", attributeResults)
	dependencyFailure, dependencyError := printResults("Required dependencies", dependencyResults)
	hasFailure = hasFailure || dependencyFailure
	hasError = hasError || dependencyError

	switch {
	case hasError:
		fmt.Println("RESULT: ERROR")
		os.Exit(2)
	case hasFailure:
		fmt.Println("RESULT: NOT READY")
		os.Exit(1)
	default:
		fmt.Println("RESULT: READY")
	}
}

func loadContract(path string) (contract, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return contract{}, fmt.Errorf("read contract: %w", err)
	}
	defer file.Close()

	var c contract
	section := ""
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "service:"):
			c.Service = strings.TrimSpace(strings.TrimPrefix(line, "service:"))
			section = ""
		case line == "required_attributes:":
			section = "attributes"
		case line == "required_dependencies:":
			section = "dependencies"
		case strings.HasPrefix(line, "- "):
			value := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			switch section {
			case "attributes":
				c.RequiredAttributes = append(c.RequiredAttributes, value)
			case "dependencies":
				c.RequiredDependencies = append(c.RequiredDependencies, value)
			default:
				return contract{}, fmt.Errorf("contract line %d: list item outside a supported section", lineNumber)
			}
		default:
			return contract{}, fmt.Errorf("contract line %d: unsupported YAML", lineNumber)
		}
	}
	if err := scanner.Err(); err != nil {
		return contract{}, fmt.Errorf("read contract: %w", err)
	}
	if c.Service == "" || len(c.RequiredAttributes) == 0 || len(c.RequiredDependencies) == 0 {
		return contract{}, errors.New("contract requires service, required_attributes, and required_dependencies")
	}
	for _, value := range append(append([]string{c.Service}, c.RequiredAttributes...), c.RequiredDependencies...) {
		if !contractValuePattern.MatchString(value) {
			return contract{}, fmt.Errorf("contract value %q contains unsupported characters", value)
		}
	}
	return c, nil
}

func newNerdGraphClientFromEnvironment() (*nerdGraphClient, error) {
	userKey := os.Getenv("NEW_RELIC_USER_KEY")
	if userKey == "" {
		return nil, errors.New("NEW_RELIC_USER_KEY is required")
	}
	accountIDText := os.Getenv("NEW_RELIC_ACCOUNT_ID")
	if accountIDText == "" {
		return nil, errors.New("NEW_RELIC_ACCOUNT_ID is required")
	}
	accountID, err := strconv.ParseInt(accountIDText, 10, 64)
	if err != nil || accountID <= 0 {
		return nil, errors.New("NEW_RELIC_ACCOUNT_ID must be a positive integer")
	}
	endpoint := os.Getenv("NEW_RELIC_NERDGRAPH_ENDPOINT")
	if endpoint == "" {
		return nil, errors.New("NEW_RELIC_NERDGRAPH_ENDPOINT is required")
	}

	return &nerdGraphClient{
		endpoint:  endpoint,
		userKey:   userKey,
		accountID: accountID,
		http:      &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *nerdGraphClient) attributeExists(service, attribute, demoRunID string) (bool, error) {
	nrql := fmt.Sprintf(
		"FROM Transaction SELECT count(*) AS presentCount WHERE appName = '%s' AND `demo.run_id` = '%s' AND `%s` IS NOT NULL SINCE %s AGO",
		service, demoRunID, attribute, window,
	)
	results, err := c.query(nrql)
	if err != nil {
		return false, err
	}
	return resultNumber(results, "presentCount") > 0, nil
}

func (c *nerdGraphClient) dependencyExists(service, dependency, demoRunID string) (bool, error) {
	nrql := fmt.Sprintf(
		"FROM Span SELECT uniques(appName) AS apps WHERE trace.id IN (SELECT uniques(trace.id) FROM Span WHERE appName = '%s' AND `demo.run_id` = '%s') FACET trace.id SINCE %s AGO LIMIT MAX",
		service, demoRunID, window,
	)
	results, err := c.query(nrql)
	if err != nil {
		return false, err
	}
	for _, result := range results {
		if containsString(result["apps"], dependency) {
			return true, nil
		}
	}
	return false, nil
}

func (c *nerdGraphClient) query(nrql string) ([]map[string]any, error) {
	payload := graphQLRequest{
		Query: `query ReadinessNRQL($accountId: Int!, $nrql: Nrql!) {
  actor {
    account(id: $accountId) {
      nrql(query: $nrql) { results }
    }
  }
}`,
		Variables: map[string]any{"accountId": c.accountID, "nrql": nrql},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode NerdGraph request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create NerdGraph request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-Key", c.userKey)

	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("NerdGraph request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read NerdGraph response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, errors.New("New Relic API authentication failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("NerdGraph returned HTTP %d", response.StatusCode)
	}

	var decoded graphQLResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, errors.New("NerdGraph returned invalid JSON")
	}
	if len(decoded.Errors) > 0 {
		message := decoded.Errors[0].Message
		lowerMessage := strings.ToLower(message)
		if strings.Contains(lowerMessage, "api key") || strings.Contains(lowerMessage, "unauthorized") || strings.Contains(lowerMessage, "authentication") {
			return nil, errors.New("New Relic API authentication failed")
		}
		return nil, fmt.Errorf("NerdGraph query failed: %s", message)
	}
	if decoded.Data.Actor.Account == nil {
		return nil, errors.New("NerdGraph response did not contain the requested account")
	}
	return decoded.Data.Actor.Account.NRQL.Results, nil
}

func resultNumber(results []map[string]any, key string) float64 {
	if len(results) == 0 {
		return 0
	}
	return number(results[0][key])
}

func number(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case json.Number:
		number, _ := value.Float64()
		return number
	default:
		return 0
	}
}

func containsString(value any, expected string) bool {
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if text, ok := value.(string); ok && text == expected {
			return true
		}
	}
	return false
}

func printHeader(c contract, demoRunID string) {
	fmt.Println("=== Observability Readiness Check ===")
	fmt.Println()
	fmt.Printf("Service: %s\n", c.Service)
	if demoRunID != "" {
		fmt.Printf("Run ID: %s\n", demoRunID)
	}
	fmt.Printf("Window: last %s\n\n", window)
}

func printResults(title string, results []checkResult) (hasFailure, hasError bool) {
	fmt.Println(title)
	for _, result := range results {
		switch {
		case result.Err != nil:
			fmt.Printf("[ERROR] %s: %v\n", result.Name, result.Err)
			hasError = true
		case result.Passed:
			fmt.Printf("[PASS] %s\n", result.Name)
		default:
			fmt.Printf("[FAIL] %s\n", result.Name)
			hasFailure = true
		}
	}
	fmt.Println()
	return hasFailure, hasError
}

func printErrorAndExit(message string) {
	fmt.Println("=== Observability Readiness Check ===")
	fmt.Printf("\n[ERROR] %s\n\nRESULT: ERROR\n", message)
	os.Exit(2)
}
