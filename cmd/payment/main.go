package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/newrelic/go-agent/v3/newrelic"
)

type paymentRequest struct {
	TenantID     string `json:"tenantId"`
	CustomerPlan string `json:"customerPlan"`
	Amount       int64  `json:"amount"`
}

type paymentResponse struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

func main() {
	port := envOrDefault("PORT", "8081")
	demoRunID := os.Getenv("DEMO_RUN_ID")

	app, err := newrelic.NewApplication(newrelic.ConfigFromEnvironment())
	if err != nil {
		log.Fatalf("failed to initialize New Relic application: %v", err)
	}
	defer app.Shutdown(5 * time.Second)

	mux := http.NewServeMux()
	_, instrumentedPay := newrelic.WrapHandleFunc(app, "/pay", paymentHandler(demoRunID))
	mux.HandleFunc("POST /pay", instrumentedPay)

	log.Printf("payment-service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func paymentHandler(demoRunID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request paymentRequest
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
		txn.AddAttribute("demo.run_id", demoRunID)

		writeJSON(w, http.StatusOK, paymentResponse{Success: true, Status: "paid"})
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
