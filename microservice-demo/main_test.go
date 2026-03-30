package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func resetTrafficStateForTest() {
	captureMu.Lock()
	capturedTraffic = nil
	captureMu.Unlock()
	captureSeq.Store(0)

	countersMu.Lock()
	counters = map[string]*simpleCounter{}
	countersMu.Unlock()
}

func TestTrafficCaptureMiddlewareCapturesDemoRequests(t *testing.T) {
	resetTrafficStateForTest()

	h := trafficCaptureMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/demo/action?userId=alice", strings.NewReader("{\"hello\":\"world\"}"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	captureMu.Lock()
	defer captureMu.Unlock()
	if len(capturedTraffic) != 1 {
		t.Fatalf("expected 1 captured request, got %d", len(capturedTraffic))
	}
	got := capturedTraffic[0]
	if got.Method != http.MethodPost {
		t.Fatalf("expected method POST, got %s", got.Method)
	}
	if got.Path != "/demo/action" {
		t.Fatalf("expected path /demo/action, got %s", got.Path)
	}
	if got.Query != "userId=alice" {
		t.Fatalf("unexpected query: %s", got.Query)
	}
	if got.Headers["Content-Type"] != "application/json" {
		t.Fatalf("expected Content-Type header to be captured")
	}
}

func TestGetCapturedTrafficHandlerRespectsLimit(t *testing.T) {
	resetTrafficStateForTest()
	captureMu.Lock()
	capturedTraffic = []capturedRequest{
		{ID: 1, Method: http.MethodPost, Path: "/demo/action"},
		{ID: 2, Method: http.MethodPost, Path: "/demo/action"},
		{ID: 3, Method: http.MethodPost, Path: "/demo/action"},
	}
	captureMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/demo/traffic/captured?limit=2", nil)
	rr := httptest.NewRecorder()
	getCapturedTrafficHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		TotalCaptured int               `json:"totalCaptured"`
		Returned      int               `json:"returned"`
		Items         []capturedRequest `json:"items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TotalCaptured != 3 {
		t.Fatalf("expected totalCaptured=3, got %d", resp.TotalCaptured)
	}
	if resp.Returned != 2 {
		t.Fatalf("expected returned=2, got %d", resp.Returned)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	if resp.Items[0].ID != 2 || resp.Items[1].ID != 3 {
		t.Fatalf("expected IDs [2,3], got [%d,%d]", resp.Items[0].ID, resp.Items[1].ID)
	}
}

func TestReplayTrafficHandlerReplaysMatchingRequests(t *testing.T) {
	resetTrafficStateForTest()

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo/action" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("extract test server port: %v", err)
	}
	demoPort = port

	captureMu.Lock()
	capturedTraffic = []capturedRequest{
		{ID: 1, Method: http.MethodPost, Path: "/demo/action", Query: "userId=alice", Headers: map[string]string{"Content-Type": "application/json"}},
		{ID: 2, Method: http.MethodPost, Path: "/demo/action", Query: "userId=bob", Headers: map[string]string{"Content-Type": "application/json"}},
		{ID: 3, Method: http.MethodGet, Path: "/demo/hello"},
	}
	captureMu.Unlock()

	body := strings.NewReader(`{"path":"/demo/action","method":"POST","limit":10}`)
	req := httptest.NewRequest(http.MethodPost, "/demo/traffic/replay", body)
	rr := httptest.NewRecorder()
	replayTrafficHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		Path         string         `json:"path"`
		Method       string         `json:"method"`
		Attempted    int            `json:"attempted"`
		Successful   int            `json:"successful"`
		Failed       int            `json:"failed"`
		StatusCounts map[string]int `json:"statusCounts"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Attempted != 2 {
		t.Fatalf("expected attempted=2, got %d", resp.Attempted)
	}
	if resp.Successful != 2 {
		t.Fatalf("expected successful=2, got %d", resp.Successful)
	}
	if resp.Failed != 0 {
		t.Fatalf("expected failed=0, got %d", resp.Failed)
	}
	if resp.StatusCounts["200"] != 2 {
		t.Fatalf("expected statusCounts[200]=2, got %d", resp.StatusCounts["200"])
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 replayed requests, got %d", calls.Load())
	}
}
