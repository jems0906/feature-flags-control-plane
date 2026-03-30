package sdk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecordConversionAndReportCircuitBreakerUseBearerToken(t *testing.T) {
	var seenConversionAuth string
	var seenBreakerAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/experiment/button-color/convert":
			seenConversionAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		case "/circuitbreaker/report":
			seenBreakerAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewSDKClient(server.URL, 0)
	client.SetAuthToken("secret-token")

	if err := client.RecordConversion("button-color", "green"); err != nil {
		t.Fatalf("record conversion: %v", err)
	}
	if err := client.ReportCircuitBreakerResult("/demo/action", false, 320); err != nil {
		t.Fatalf("report circuit breaker: %v", err)
	}

	if seenConversionAuth != "Bearer secret-token" {
		t.Fatalf("expected conversion auth header, got %q", seenConversionAuth)
	}
	if seenBreakerAuth != "Bearer secret-token" {
		t.Fatalf("expected breaker auth header, got %q", seenBreakerAuth)
	}
}

func TestCheckCircuitBreakerParsesAdmissionResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/circuitbreaker/check" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"route":   "/demo/action",
			"allowed": false,
			"state":   "half-open",
		})
	}))
	defer server.Close()

	client := NewSDKClient(server.URL, 0)
	allowed, state, err := client.CheckCircuitBreaker("/demo/action")
	if err != nil {
		t.Fatalf("check circuit breaker: %v", err)
	}
	if allowed {
		t.Fatal("expected breaker admission to reject the request")
	}
	if state != "half-open" {
		t.Fatalf("expected half-open state, got %q", state)
	}
}