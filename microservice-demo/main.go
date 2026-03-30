// microservice-demo is a sample service that demonstrates how a real
// microservice integrates with the Feature Flags & Traffic Control Platform.
//
// Endpoints:
//
//	GET /demo/hello?userId=<id>       - feature-flag gated greeting
//	GET /demo/experiment?userId=<id>  - A/B experiment variant assignment
//	POST /demo/action?userId=<id>     - rate-limited, circuit-broken endpoint
//	GET /demo/traffic/captured        - inspect captured request samples
//	POST /demo/traffic/replay         - replay captured traffic to an endpoint
//	GET  /demo/health
//	GET  /metrics                     - Prometheus text-format metrics
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var controlPlaneURL string
var controlPlaneAuthToken string
var demoPort string

const maxCapturedTraffic = 2000

type capturedRequest struct {
	ID        int64             `json:"id"`
	Timestamp string            `json:"timestamp"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Query     string            `json:"query"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body,omitempty"`
}

type replayTrafficRequest struct {
	Path    string `json:"path"`
	Method  string `json:"method"`
	Limit   int    `json:"limit"`
	DelayMs int    `json:"delayMs"`
}

var (
	captureMu       sync.Mutex
	capturedTraffic []capturedRequest
	captureSeq      atomic.Int64
)

// ---------------------------------------------------------------------------
// Minimal inline metrics (Prometheus text format, stdlib only)
// ---------------------------------------------------------------------------

type simpleCounter struct{ n atomic.Int64 }

func (c *simpleCounter) Inc()        { c.n.Add(1) }
func (c *simpleCounter) Load() int64 { return c.n.Load() }

var (
	countersMu sync.Mutex
	counters   = map[string]*simpleCounter{}
)

func counter(name string) *simpleCounter {
	countersMu.Lock()
	defer countersMu.Unlock()
	if c, ok := counters[name]; ok {
		return c
	}
	c := &simpleCounter{}
	counters[name] = c
	return c
}

func serveMetrics(w http.ResponseWriter, _ *http.Request) {
	var sb strings.Builder
	countersMu.Lock()
	for name, c := range counters {
		fmt.Fprintf(&sb, "# TYPE %s counter\n%s %d\n", name, name, c.Load())
	}
	countersMu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprint(w, sb.String())
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	controlPlaneURL = os.Getenv("CONTROL_PLANE_URL")
	if controlPlaneURL == "" {
		controlPlaneURL = "http://localhost:8080"
	}
	controlPlaneAuthToken = strings.TrimSpace(os.Getenv("CONTROL_PLANE_AUTH_TOKEN"))
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	demoPort = port

	// Seed demo data into the control-plane (best-effort, on a small delay).
	go func() {
		time.Sleep(2 * time.Second)
		seedDemoData()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", demoLandingHandler)
	mux.HandleFunc("GET /demo/hello", helloHandler)
	mux.HandleFunc("GET /demo/experiment", experimentHandler)
	mux.HandleFunc("POST /demo/action", actionHandler)
	mux.HandleFunc("GET /demo/traffic/captured", getCapturedTrafficHandler)
	mux.HandleFunc("POST /demo/traffic/replay", replayTrafficHandler)
	mux.HandleFunc("GET /demo/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /metrics", serveMetrics)
	server := &http.Server{Addr: ":" + port, Handler: trafficCaptureMiddleware(mux)}

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdownSignals
		log.Println("Shutdown signal received; draining microservice-demo")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown error: %v", err)
		}
	}()

	log.Printf("microservice-demo listening on :%s (control-plane=%s)", port, controlPlaneURL)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Demo data seeder
// ---------------------------------------------------------------------------

func seedDemoData() {
	postJSON(controlPlaneURL+"/flags", map[string]interface{}{
		"Name":        "dark-mode",
		"Enabled":     true,
		"Environment": "dev",
		"TargetRules": []map[string]interface{}{
			{"Type": "percentage", "Rollout": 50},
		},
	})
	postJSON(controlPlaneURL+"/experiment", map[string]interface{}{
		"Name":     "button-color",
		"Variants": []string{"blue", "green"},
	})
	postJSON(controlPlaneURL+"/ratelimit", map[string]interface{}{
		"Route": "/demo/action",
		"Limit": 10,
	})
	postJSON(controlPlaneURL+"/circuitbreaker", map[string]interface{}{
		"Route":              "/demo/action",
		"State":              "closed",
		"ErrorThreshold":     0.5,
		"LatencyThresholdMs": 250,
	})
	log.Println("Demo data seeded into control-plane")
}

func postJSON(url string, payload interface{}) {
	var body io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		log.Printf("seed build request: %v", err)
		return
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	setControlPlaneAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("seed POST %s: %v", url, err)
		return
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// Landing page
// ---------------------------------------------------------------------------

func demoLandingHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	cp := controlPlaneURL
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><title>Microservice Demo</title>
<style>
body{font-family:system-ui,sans-serif;max-width:820px;margin:40px auto;padding:0 20px;background:#0f172a;color:#e2e8f0}
h1{color:#a78bfa;margin-bottom:4px}h2{color:#94a3b8;font-size:.95rem;font-weight:400;margin-top:0}
.card{background:#1e293b;border-radius:8px;padding:16px 20px;margin:12px 0}
.card h3{margin:0 0 10px;color:#c4b5fd;font-size:.95rem}
a.btn{display:inline-block;background:#4c1d95;color:#ddd6fe;padding:6px 14px;border-radius:6px;text-decoration:none;font-size:.85rem;margin:4px 4px 0 0}
a.btn:hover{background:#5b21b6}
code{background:#0f172a;padding:2px 7px;border-radius:4px;font-size:.85rem;color:#7dd3fc}
.cp{color:#64748b;font-size:.8rem}
</style></head>
<body>
<h1>&#127881; Microservice Demo</h1>
<h2>Integrated with control-plane at <code>%s</code></h2>

<div class="card">
  <h3>&#127988; Feature Flag — dark-mode (50%% rollout)</h3>
  <a class="btn" href="/demo/hello?userId=alice">alice</a>
  <a class="btn" href="/demo/hello?userId=bob">bob</a>
  <a class="btn" href="/demo/hello?userId=charlie">charlie</a>
  <a class="btn" href="/demo/hello?userId=diana">diana</a>
  <p class="cp">GET /demo/hello?userId=&lt;id&gt;</p>
</div>

<div class="card">
  <h3>&#127914; A/B Experiment — button-color</h3>
  <a class="btn" href="/demo/experiment?userId=alice">alice</a>
  <a class="btn" href="/demo/experiment?userId=bob">bob</a>
  <a class="btn" href="/demo/experiment?userId=charlie">charlie</a>
  <a class="btn" href="/demo/experiment?userId=diana">diana</a>
  <p class="cp">GET /demo/experiment?userId=&lt;id&gt;</p>
</div>

<div class="card">
  <h3>&#128202; Observability</h3>
  <a class="btn" href="/metrics">Prometheus metrics</a>
  <a class="btn" href="/demo/health">Health check</a>
  <a class="btn" href="%s">Control plane &#8599;</a>
  <a class="btn" href="%s/flags/all">All flags &#8599;</a>
</div>

<div class="card">
  <h3>&#9889; Rate-limited + circuit-broken action</h3>
	<p style="color:#94a3b8;font-size:.85rem">POST /demo/action?userId=&lt;id&gt;&amp;fail=true&amp;sleepMs=300 — use curl or Postman</p>
  <code>curl -X POST "http://localhost:8081/demo/action?userId=alice"</code>
</div>

<div class="card">
  <h3>&#128257; Traffic capture and replay</h3>
  <a class="btn" href="/demo/traffic/captured?limit=25">Captured requests</a>
  <p class="cp">POST /demo/traffic/replay with {"path":"/demo/action","method":"POST","limit":10}</p>
</div>
</body></html>`, cp, cp, cp)
}

// ---------------------------------------------------------------------------
// Traffic capture and replay
// ---------------------------------------------------------------------------

func trafficCaptureMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captureRequest(r)
		next.ServeHTTP(w, r)
	})
}

func captureRequest(r *http.Request) {
	if r == nil {
		return
	}
	if r.Header.Get("X-Traffic-Replay") == "true" {
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/demo/") {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/demo/traffic/") || r.URL.Path == "/demo/health" {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		return
	}
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	req := capturedRequest{
		ID:        captureSeq.Add(1),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Method:    r.Method,
		Path:      r.URL.Path,
		Query:     r.URL.RawQuery,
		Headers:   captureHeaders(r.Header),
		Body:      string(body),
	}

	captureMu.Lock()
	if len(capturedTraffic) >= maxCapturedTraffic {
		copy(capturedTraffic, capturedTraffic[1:])
		capturedTraffic[len(capturedTraffic)-1] = req
	} else {
		capturedTraffic = append(capturedTraffic, req)
	}
	captureMu.Unlock()
	counter("demo_traffic_captured_total").Inc()
}

func captureHeaders(h http.Header) map[string]string {
	out := make(map[string]string)
	for _, key := range []string{"Content-Type", "User-Agent", "X-Request-Id"} {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			out[key] = v
		}
	}
	return out
}

func getCapturedTrafficHandler(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	captureMu.Lock()
	total := len(capturedTraffic)
	start := total - limit
	if start < 0 {
		start = 0
	}
	items := append([]capturedRequest(nil), capturedTraffic[start:]...)
	captureMu.Unlock()

	writeJSON(w, map[string]interface{}{
		"totalCaptured": total,
		"returned":      len(items),
		"items":         items,
	})
}

func replayTrafficHandler(w http.ResponseWriter, r *http.Request) {
	var req replayTrafficRequest
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}

	if req.Path == "" {
		req.Path = "/demo/action"
	}
	if req.Method == "" {
		req.Method = http.MethodPost
	}
	req.Method = strings.ToUpper(req.Method)
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 500 {
		req.Limit = 500
	}
	if req.DelayMs < 0 {
		req.DelayMs = 0
	}

	selected := selectCapturedTraffic(req.Path, req.Method, req.Limit)
	if len(selected) == 0 {
		writeJSON(w, map[string]interface{}{
			"path":         req.Path,
			"method":       req.Method,
			"attempted":    0,
			"successful":   0,
			"failed":       0,
			"statusCounts": map[string]int{},
			"message":      "no matching captured traffic",
		})
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	statusCounts := make(map[string]int)
	failed := 0

	for _, item := range selected {
		target := fmt.Sprintf("http://127.0.0.1:%s%s", demoPort, item.Path)
		if item.Query != "" {
			target += "?" + item.Query
		}

		replayReq, err := http.NewRequest(item.Method, target, strings.NewReader(item.Body))
		if err != nil {
			failed++
			counter("demo_traffic_replay_errors_total").Inc()
			continue
		}
		for k, v := range item.Headers {
			replayReq.Header.Set(k, v)
		}
		replayReq.Header.Set("X-Traffic-Replay", "true")

		resp, err := client.Do(replayReq)
		if err != nil {
			failed++
			counter("demo_traffic_replay_errors_total").Inc()
		} else {
			statusKey := strconv.Itoa(resp.StatusCode)
			statusCounts[statusKey]++
			counter("demo_traffic_replayed_total").Inc()
			resp.Body.Close()
		}

		if req.DelayMs > 0 {
			time.Sleep(time.Duration(req.DelayMs) * time.Millisecond)
		}
	}

	writeJSON(w, map[string]interface{}{
		"path":         req.Path,
		"method":       req.Method,
		"attempted":    len(selected),
		"successful":   len(selected) - failed,
		"failed":       failed,
		"statusCounts": statusCounts,
	})
}

func selectCapturedTraffic(path, method string, limit int) []capturedRequest {
	captureMu.Lock()
	defer captureMu.Unlock()

	matches := make([]capturedRequest, 0, len(capturedTraffic))
	for _, req := range capturedTraffic {
		if req.Path == path && req.Method == method {
			matches = append(matches, req)
		}
	}

	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	if len(matches) > limit {
		matches = matches[len(matches)-limit:]
	}
	return matches
}

// ---------------------------------------------------------------------------
// /demo/hello
// ---------------------------------------------------------------------------

func helloHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		userID = "anonymous"
	}
	enabled, err := evaluateFlag("dark-mode", userID)
	if err != nil {
		log.Printf("hello: flag eval error: %v", err)
	}
	// Track outcome in inline metrics
	outcome := "off"
	if enabled {
		outcome = "on"
	}
	counter("demo_dark_mode_" + outcome).Inc()

	greeting := "Hello, " + userID + "! (light mode)"
	if enabled {
		greeting = "Hello, " + userID + "! \U0001f319 (dark mode)"
	}
	writeJSON(w, map[string]interface{}{
		"userId":    userID,
		"greeting":  greeting,
		"darkMode":  enabled,
		"flagError": errMsg(err),
	})
}

// ---------------------------------------------------------------------------
// /demo/experiment
// ---------------------------------------------------------------------------

func experimentHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		userID = "anonymous"
	}
	variant, err := getVariant("button-color", userID)
	if err != nil {
		log.Printf("experiment: variant error: %v", err)
		variant = "blue"
	}
	counter("demo_experiment_variant_" + variant).Inc()
	writeJSON(w, map[string]interface{}{
		"userId":     userID,
		"experiment": "button-color",
		"variant":    variant,
		"message":    fmt.Sprintf("Show %s button to %s", variant, userID),
	})
}

// ---------------------------------------------------------------------------
// /demo/action  - rate limiting + circuit breaking enforced
// ---------------------------------------------------------------------------

func actionHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		userID = "anonymous"
	}
	shouldFail := strings.EqualFold(r.URL.Query().Get("fail"), "true")
	sleepMs, _ := strconv.Atoi(r.URL.Query().Get("sleepMs"))

	// 1. Circuit breaker
	allowedByCircuit, cbState, err := checkCircuitBreaker("/demo/action")
	if err != nil {
		log.Printf("action: cb error: %v", err)
	}
	if !allowedByCircuit {
		counter("demo_action_circuit_open").Inc()
		http.Error(w, "service unavailable (circuit open)", http.StatusServiceUnavailable)
		return
	}

	// 2. Rate limiter
	allowed, err := checkRateLimit("/demo/action", userID, "")
	if err != nil {
		log.Printf("action: rate limit error: %v", err)
		allowed = true // fail open
	}
	if !allowed {
		counter("demo_action_throttled").Inc()
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	if sleepMs > 0 {
		time.Sleep(time.Duration(sleepMs) * time.Millisecond)
	}
	latencyMs := int(time.Since(start).Milliseconds())
	if shouldFail {
		reportCircuitBreakerResult("/demo/action", false, latencyMs)
		counter("demo_action_failure").Inc()
		http.Error(w, "downstream error", http.StatusInternalServerError)
		return
	}
	reportCircuitBreakerResult("/demo/action", true, latencyMs)

	counter("demo_action_ok").Inc()
	writeJSON(w, map[string]interface{}{
		"userId":    userID,
		"result":    "action performed",
		"cbState":   cbState,
		"latencyMs": latencyMs,
	})
}

// ---------------------------------------------------------------------------
// Control-plane API helpers
// ---------------------------------------------------------------------------

func evaluateFlag(flagName, userID string) (bool, error) {
	payload := map[string]interface{}{"UserID": userID, "Headers": map[string]string{}}
	data, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/flags/%s/evaluate", controlPlaneURL, flagName)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data)) //nolint:gosec
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	enabled, _ := result["enabled"].(bool)
	return enabled, nil
}

func getVariant(experimentName, userID string) (string, error) {
	url := fmt.Sprintf("%s/experiment/%s/variant?userId=%s", controlPlaneURL, experimentName, userID)
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result["variant"], nil
}

func checkRateLimit(route, userID, tenantID string) (bool, error) {
	payload := map[string]string{"route": route, "userId": userID, "tenantId": tenantID}
	data, _ := json.Marshal(payload)
	resp, err := http.Post(controlPlaneURL+"/ratelimit/check", "application/json", bytes.NewReader(data)) //nolint:gosec
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return true, err
	}
	allowed, _ := result["allowed"].(bool)
	return allowed, nil
}

func getCircuitBreakerState(route string) (string, error) {
	endpoint := fmt.Sprintf("%s/circuitbreaker?route=%s", controlPlaneURL, url.QueryEscape(route))
	resp, err := http.Get(endpoint) //nolint:gosec
	if err != nil {
		return "closed", err
	}
	defer resp.Body.Close()
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "closed", err
	}
	return result["state"], nil
}

func checkCircuitBreaker(route string) (bool, string, error) {
	payload := map[string]string{"route": route}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, controlPlaneURL+"/circuitbreaker/check", bytes.NewReader(data))
	if err != nil {
		return true, "closed", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return true, "closed", err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return true, "closed", err
	}
	allowed, _ := result["allowed"].(bool)
	state, _ := result["state"].(string)
	if state == "" {
		state = "closed"
	}
	return allowed, state, nil
}

func reportCircuitBreakerResult(route string, success bool, latencyMs int) {
	payload := map[string]interface{}{
		"route":     route,
		"success":   success,
		"latencyMs": latencyMs,
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, controlPlaneURL+"/circuitbreaker/report", bytes.NewReader(data))
	if err != nil {
		log.Printf("report circuit breaker build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	setControlPlaneAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("report circuit breaker: %v", err)
		return
	}
	resp.Body.Close()
}

func setControlPlaneAuth(req *http.Request) {
	if controlPlaneAuthToken == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+controlPlaneAuthToken)
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
