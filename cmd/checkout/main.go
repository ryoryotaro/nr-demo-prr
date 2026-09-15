package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/newrelic/go-agent/v3/newrelic"
)

type checkoutRequest struct {
	TenantID     string `json:"tenantId"`
	CustomerPlan string `json:"customerPlan"`
	Amount       int64  `json:"amount"`
}

type paymentResponse struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

type checkoutResponse struct {
	Success       bool   `json:"success"`
	Status        string `json:"status"`
	PaymentStatus string `json:"paymentStatus"`
}

func main() {
	port := envOrDefault("PORT", "8080")
	paymentURL := envOrDefault("PAYMENT_URL", "http://localhost:8081/pay")
	observabilityMode := envOrDefault("OBSERVABILITY_MODE", "complete")
	demoRunID := os.Getenv("DEMO_RUN_ID")
	if observabilityMode != "complete" && observabilityMode != "incomplete" && observabilityMode != "regression" {
		log.Fatalf("invalid OBSERVABILITY_MODE %q: use complete, incomplete, or regression", observabilityMode)
	}

	app, err := newrelic.NewApplication(newrelic.ConfigFromEnvironment())
	if err != nil {
		log.Fatalf("failed to initialize New Relic application: %v", err)
	}
	defer app.Shutdown(5 * time.Second)

	transport := http.DefaultTransport
	client := &http.Client{Timeout: 3 * time.Second, Transport: transport}

	mux := http.NewServeMux()
	_, instrumentedCheckout := newrelic.WrapHandleFunc(app, "/checkout", checkoutHandler(client, paymentURL, observabilityMode, demoRunID))
	mux.HandleFunc("POST /checkout", instrumentedCheckout)

	log.Printf("checkout-service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func checkoutHandler(client *http.Client, paymentURL, observabilityMode, demoRunID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request checkoutRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
			return
		}
		if request.TenantID == "" || request.CustomerPlan == "" || request.Amount <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenantId, customerPlan, and a positive amount are required"})
			return
		}

		txn := newrelic.FromContext(r.Context())
		txn.AddAttribute("tenant.id", request.TenantID)
		txn.AddAttribute("demo.run_id", demoRunID)

		body, err := json.Marshal(request)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare payment"})
			return
		}
		paymentRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, paymentURL, bytes.NewReader(body))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare payment"})
			return
		}
		paymentRequest.Header.Set("Content-Type", "application/json")

		response, err := client.Do(paymentRequest)
		if err != nil {
			log.Printf("payment request failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "payment-service is unavailable"})
			return
		}
		defer response.Body.Close()

		if response.StatusCode != http.StatusOK {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("payment-service returned HTTP %d", response.StatusCode)})
			return
		}
		var payment paymentResponse
		if err := json.NewDecoder(response.Body).Decode(&payment); err != nil || !payment.Success {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "payment was not successful"})
			return
		}

		writeJSON(w, http.StatusOK, checkoutResponse{
			Success:       true,
			Status:        "checkout_completed",
			PaymentStatus: payment.Status,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
