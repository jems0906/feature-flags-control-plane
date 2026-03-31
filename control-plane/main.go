package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	flagsMu sync.RWMutex
	flags   = make(map[string]*FeatureFlag)

	expsMu      sync.RWMutex
	experiments = make(map[string]*Experiment)

	cbMu            sync.RWMutex
	circuitBreakers = make(map[string]*CircuitBreaker)

	rlMu       sync.RWMutex
	rateLimits = make(map[string]int)

	store *ConfigStore

	sseClientsMu sync.RWMutex
	sseClients   = make(map[string][]chan string)
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Printf("warning: could not create data dir %s: %v", dataDir, err)
	}
	InitMetrics()
	InitTracing()
	store = NewConfigStore(dataDir + "/config.json")
	loadFromStore(ctx)
	log.Println("Loaded persisted config from store")
	startStoreSync(ctx, 2*time.Second)
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", controlPlaneLandingHandler)
	mux.HandleFunc("GET /metrics", ServeMetrics)
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("POST /flags", createFlagHandler)
	mux.HandleFunc("GET /flags/all", getAllFlagsHandler)
	mux.HandleFunc("GET /flags/stream", flagsSSEHandler)
	mux.HandleFunc("POST /flags/{name}/evaluate", evaluateFlagHandler)
	mux.HandleFunc("GET /flags/{name}", getFlagHandler)
	mux.HandleFunc("PUT /flags/{name}", updateFlagHandler)
	mux.HandleFunc("DELETE /flags/{name}", deleteFlagHandler)
	mux.HandleFunc("POST /experiment", createExperimentHandler)
	mux.HandleFunc("GET /experiment/{name}/variant", getVariantHandler)
	mux.HandleFunc("POST /experiment/{name}/convert", recordConversionHandler)
	mux.HandleFunc("POST /ratelimit", setRateLimitHandler)
	mux.HandleFunc("POST /ratelimit/check", checkRateLimitHandler)
	mux.HandleFunc("GET /ratelimit", getRateLimitHandler)
	mux.HandleFunc("GET /ratelimit/{route}", getRateLimitHandler)
	mux.HandleFunc("POST /circuitbreaker", setCircuitBreakerHandler)
	mux.HandleFunc("POST /circuitbreaker/check", checkCircuitBreakerHandler)
	mux.HandleFunc("POST /circuitbreaker/report", reportCircuitBreakerHandler)
	mux.HandleFunc("GET /circuitbreaker", getCircuitBreakerHandler)
	mux.HandleFunc("GET /circuitbreaker/{route}", getCircuitBreakerHandler)
	mux.HandleFunc("POST /config", setConfigHandler)
	mux.HandleFunc("GET /config/{key}", getConfigHandler)

	handler := metricsMiddleware(tracingMiddleware(authMiddleware(mux)))
	server := &http.Server{Addr: ":" + port, Handler: handler}

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdownSignals
		log.Println("Shutdown signal received; draining control-plane")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown error: %v", err)
		}
	}()

	log.Printf("Control plane listening on :%s", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func controlPlaneLandingHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><title>Control Plane</title>
<style>
body{font-family:system-ui,sans-serif;max-width:860px;margin:40px auto;padding:0 20px;background:#0f172a;color:#e2e8f0}
h1{color:#38bdf8;margin-bottom:4px}h2{color:#94a3b8;font-size:.95rem;font-weight:400;margin-top:0}
table{width:100%;border-collapse:collapse;margin:16px 0}
th{text-align:left;padding:8px 12px;background:#1e293b;color:#94a3b8;font-size:.8rem;text-transform:uppercase;letter-spacing:.05em}
td{padding:8px 12px;border-bottom:1px solid #1e293b;font-size:.9rem}
code{background:#1e293b;padding:2px 6px;border-radius:4px;font-size:.85rem;color:#7dd3fc}
.method{font-weight:700;font-size:.75rem;padding:2px 7px;border-radius:4px;display:inline-block}
.get{background:#064e3b;color:#6ee7b7}.post{background:#1e3a5f;color:#93c5fd}.put{background:#451a03;color:#fdba74}.del{background:#4c0519;color:#fca5a5}
a{color:#38bdf8}
</style></head>
<body>
<h1>&#9889; Control Plane</h1>
<h2>Feature Flag &amp; Traffic Control Platform &mdash; <a href="/health">/health</a> &bull; <a href="/metrics">/metrics</a> &bull; <a href="/flags/all">browse flags</a></h2>
<table>
<tr><th>Method</th><th>Path</th><th>Description</th></tr>
<tr><td><span class="method get">GET</span></td><td><code>/health</code></td><td>Service health check</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/metrics</code></td><td>Prometheus metrics</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/flags</code></td><td>Create / update a feature flag</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/flags/all</code></td><td>List all flags</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/flags/{name}</code></td><td>Get a flag by name</td></tr>
<tr><td><span class="method put">PUT</span></td><td><code>/flags/{name}</code></td><td>Update a flag</td></tr>
<tr><td><span class="method del">DELETE</span></td><td><code>/flags/{name}</code></td><td>Delete a flag</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/flags/{name}/evaluate</code></td><td>Evaluate flag for a user context</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/flags/stream?env=</code></td><td>SSE hot-reload stream</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/experiment</code></td><td>Create / update an experiment</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/experiment/{name}/variant?userId=</code></td><td>Get assigned A/B variant</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/experiment/{name}/convert?variant=</code></td><td>Record conversion event</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/ratelimit</code></td><td>Set rate limit for a route</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/ratelimit/{route}</code></td><td>View rate limit config</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/ratelimit/check</code></td><td>Check if request is allowed</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/circuitbreaker</code></td><td>Create / update circuit breaker config</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/circuitbreaker/report</code></td><td>Report request outcome and latency</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/circuitbreaker?route=</code></td><td>Get circuit breaker state</td></tr>
<tr><td><span class="method post">POST</span></td><td><code>/config</code></td><td>Store a config key/value</td></tr>
<tr><td><span class="method get">GET</span></td><td><code>/config/{key}</code></td><td>Retrieve a config value</td></tr>
</table>
<p style="color:#475569;font-size:.8rem">See <code>docs/api.md</code> for full request/response schemas.</p>
</body></html>`)
}

func loadFromStore(ctx context.Context) {
	if store == nil {
		return
	}
	before := flagPayloadsByEnv()

	flagData, err := store.GetAllByPrefix(ctx, "flag:")
	if err != nil {
		log.Printf("store sync (flags): %v", err)
		return
	}
	experimentData, err := store.GetAllByPrefix(ctx, "experiment:")
	if err != nil {
		log.Printf("store sync (experiments): %v", err)
		return
	}
	cbData, err := store.GetAllByPrefix(ctx, "cb:")
	if err != nil {
		log.Printf("store sync (circuit breakers): %v", err)
		return
	}
	rateLimitData, err := store.GetAllByPrefix(ctx, "ratelimit:")
	if err != nil {
		log.Printf("store sync (rate limits): %v", err)
		return
	}

	nextFlags := make(map[string]*FeatureFlag)
	for _, v := range flagData {
		var f FeatureFlag
		if json.Unmarshal([]byte(v), &f) == nil {
			nextFlags[f.Name] = &f
		}
	}
	nextExperiments := make(map[string]*Experiment)
	for _, v := range experimentData {
		var e Experiment
		if json.Unmarshal([]byte(v), &e) == nil {
			nextExperiments[e.Name] = &e
		}
	}
	nextCircuitBreakers := make(map[string]*CircuitBreaker)
	for _, v := range cbData {
		var cb CircuitBreaker
		if json.Unmarshal([]byte(v), &cb) == nil {
			cb.restoreRuntimeState(time.Now())
			if cb.LatencyThresholdMs == 0 {
				cb.LatencyThresholdMs = 250
			}
			nextCircuitBreakers[cb.Route] = &cb
		}
	}
	nextRateLimits := make(map[string]int)
	for k, v := range rateLimitData {
		route := strings.TrimPrefix(k, "ratelimit:")
		var limit int
		fmt.Sscan(v, &limit)
		nextRateLimits[route] = limit
	}

	flagsMu.Lock()
	flags = nextFlags
	flagsMu.Unlock()
	expsMu.Lock()
	experiments = nextExperiments
	expsMu.Unlock()
	cbMu.Lock()
	circuitBreakers = nextCircuitBreakers
	cbMu.Unlock()
	rlMu.Lock()
	rateLimits = nextRateLimits
	rlMu.Unlock()
	for route, cb := range nextCircuitBreakers {
		CircuitBreakerStateGauge.WithLabelValues(route).Set(stateToFloat(cb.GetState()))
	}
	broadcastChangedFlagPayloads(before, flagPayloadsByEnv())
}

func startStoreSync(ctx context.Context, interval time.Duration) {
	if store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				loadFromStore(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func flagPayloadsByEnv() map[string]string {
	flagsMu.RLock()
	allFlags := make([]FeatureFlag, 0, len(flags))
	envs := make(map[string]struct{})
	for _, f := range flags {
		allFlags = append(allFlags, *f)
		if f.Environment != "" {
			envs[f.Environment] = struct{}{}
		}
	}
	flagsMu.RUnlock()

	sort.Slice(allFlags, func(i, j int) bool {
		return allFlags[i].Name < allFlags[j].Name
	})
	payloads := map[string]string{"all": mustMarshalFlagList(allFlags)}
	for env := range envs {
		envFlags := make([]FeatureFlag, 0)
		for _, f := range allFlags {
			if f.Environment == env {
				envFlags = append(envFlags, f)
			}
		}
		payloads[env] = mustMarshalFlagList(envFlags)
	}
	return payloads
}

func mustMarshalFlagList(flags []FeatureFlag) string {
	data, err := json.Marshal(flags)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func broadcastChangedFlagPayloads(before, after map[string]string) {
	seen := make(map[string]struct{})
	for env, payload := range after {
		seen[env] = struct{}{}
		if before[env] != payload {
			broadcastSSE(env, payload)
		}
	}
	for env := range before {
		if _, ok := seen[env]; ok {
			continue
		}
		broadcastSSE(env, "[]")
	}
}

func broadcastCurrentFlagPayloads() {
	broadcastChangedFlagPayloads(map[string]string{}, flagPayloadsByEnv())
}

func persistJSON(ctx context.Context, prefix, name string, v interface{}) {
	if store == nil {
		return
	}
	data, _ := json.Marshal(v)
	if err := store.Set(ctx, prefix+name, string(data)); err != nil {
		log.Printf("persist error (%s%s): %v", prefix, name, err)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		dur := float64(time.Since(start).Milliseconds())
		QPS.WithLabelValues(r.URL.Path).Inc()
		Latency.WithLabelValues(r.URL.Path).Observe(dur)
		if sr.status >= 400 {
			ErrorRate.WithLabelValues(r.URL.Path).Inc()
		}
	})
}

func tracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := Tracer.Start(r.Context(), r.Method+" "+r.URL.Path)
		span.SetAttributes(strAttr("http.method", r.Method), strAttr("http.url", r.URL.String()))
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(os.Getenv("CONTROL_PLANE_AUTH_TOKEN"))
		if token == "" || !requestRequiresAuth(r) {
			next.ServeHTTP(w, r)
			return
		}
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) || strings.TrimSpace(strings.TrimPrefix(authHeader, prefix)) != token {
			w.Header().Set("WWW-Authenticate", "Bearer")
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestRequiresAuth(r *http.Request) bool {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/flags":
		return true
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/flags/"):
		return true
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/flags/"):
		return true
	case r.Method == http.MethodPost && path == "/experiment":
		return true
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/experiment/") && strings.HasSuffix(path, "/convert"):
		return true
	case r.Method == http.MethodPost && path == "/ratelimit":
		return true
	case r.Method == http.MethodPost && path == "/circuitbreaker":
		return true
	case r.Method == http.MethodPost && path == "/circuitbreaker/report":
		return true
	case r.Method == http.MethodPost && path == "/config":
		return true
	default:
		return false
	}
}

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func pathVar(r *http.Request, key string) string {
	if v := r.PathValue(key); v != "" {
		return v
	}
	parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// ---- Feature flag handlers ----

func createFlagHandler(w http.ResponseWriter, r *http.Request) {
	var f FeatureFlag
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil && err != io.EOF {
			jsonError(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	if f.Name == "" {
		f.Name = "unnamed"
	}
	flagsMu.Lock()
	flags[f.Name] = &f
	flagsMu.Unlock()
	persistJSON(r.Context(), "flag:", f.Name, f)
	broadcastCurrentFlagPayloads()
	FlagEvaluations.WithLabelValues(f.Name, "created").Inc()
	jsonOK(w, f)
}

func getAllFlagsHandler(w http.ResponseWriter, r *http.Request) {
	env := r.URL.Query().Get("env")
	flagsMu.RLock()
	result := make([]*FeatureFlag, 0, len(flags))
	for _, f := range flags {
		if env == "" || f.Environment == env {
			result = append(result, f)
		}
	}
	flagsMu.RUnlock()
	jsonOK(w, result)
}

func getFlagHandler(w http.ResponseWriter, r *http.Request) {
	name := pathVar(r, "name")
	flagsMu.RLock()
	f, ok := flags[name]
	flagsMu.RUnlock()
	if !ok {
		jsonError(w, "flag not found", http.StatusNotFound)
		return
	}
	jsonOK(w, f)
}

func updateFlagHandler(w http.ResponseWriter, r *http.Request) {
	name := pathVar(r, "name")
	var updated FeatureFlag
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	updated.Name = name
	flagsMu.Lock()
	flags[name] = &updated
	flagsMu.Unlock()
	persistJSON(r.Context(), "flag:", name, updated)
	broadcastCurrentFlagPayloads()
	jsonOK(w, updated)
}

func deleteFlagHandler(w http.ResponseWriter, r *http.Request) {
	name := pathVar(r, "name")
	flagsMu.Lock()
	delete(flags, name)
	flagsMu.Unlock()
	if store != nil {
		store.Delete(r.Context(), "flag:"+name)
	}
	broadcastCurrentFlagPayloads()
	jsonOK(w, map[string]string{"deleted": name})
}

func evaluateFlagHandler(w http.ResponseWriter, r *http.Request) {
	name := pathVar(r, "name")
	var evalCtx EvalContext
	if err := json.NewDecoder(r.Body).Decode(&evalCtx); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	flagsMu.RLock()
	f, ok := flags[name]
	flagsMu.RUnlock()
	if !ok {
		jsonError(w, "flag not found", http.StatusNotFound)
		return
	}
	enabled := f.IsFlagEnabled(evalCtx)
	result := "off"
	if enabled {
		result = "on"
	}
	FlagEvaluations.WithLabelValues(name, result).Inc()
	jsonOK(w, map[string]interface{}{"flag": name, "enabled": enabled})
}

func flagsSSEHandler(w http.ResponseWriter, r *http.Request) {
	env := r.URL.Query().Get("env")
	if env == "" {
		env = "all"
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan string, 16)
	sseClientsMu.Lock()
	sseClients[env] = append(sseClients[env], ch)
	sseClientsMu.Unlock()

	defer func() {
		sseClientsMu.Lock()
		for i, c := range sseClients[env] {
			if c == ch {
				sseClients[env] = append(sseClients[env][:i], sseClients[env][i+1:]...)
				break
			}
		}
		sseClientsMu.Unlock()
		close(ch)
	}()

	payload := flagPayloadsByEnv()[env]
	if payload == "" {
		payload = "[]"
	}
	fmt.Fprintf(w, "event: update\ndata: %s\n\n", payload)
	flusher.Flush()
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case msg, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, "event: ping\ndata: keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func broadcastSSE(env, msg string) {
	sseClientsMu.RLock()
	defer sseClientsMu.RUnlock()
	for _, e := range []string{env, "all"} {
		for _, ch := range sseClients[e] {
			select {
			case ch <- msg:
			default:
			}
		}
	}
}

// ---- Experiment handlers ----

func createExperimentHandler(w http.ResponseWriter, r *http.Request) {
	var e Experiment
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	expsMu.Lock()
	experiments[e.Name] = &e
	expsMu.Unlock()
	persistJSON(r.Context(), "experiment:", e.Name, e)
	jsonOK(w, e)
}

func getVariantHandler(w http.ResponseWriter, r *http.Request) {
	name := pathVar(r, "name")
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		jsonError(w, "userId query param required", http.StatusBadRequest)
		return
	}
	expsMu.RLock()
	e, ok := experiments[name]
	expsMu.RUnlock()
	if !ok {
		jsonError(w, "experiment not found", http.StatusNotFound)
		return
	}
	variant := e.AssignVariant(userID)
	jsonOK(w, map[string]string{"experiment": name, "variant": variant, "userId": userID})
}

func recordConversionHandler(w http.ResponseWriter, r *http.Request) {
	name := pathVar(r, "name")
	variant := r.URL.Query().Get("variant")
	if variant == "" {
		jsonError(w, "variant query param required", http.StatusBadRequest)
		return
	}
	expsMu.RLock()
	e, ok := experiments[name]
	expsMu.RUnlock()
	if !ok {
		jsonError(w, "experiment not found", http.StatusNotFound)
		return
	}
	e.RecordConversion(variant)
	jsonOK(w, map[string]string{"recorded": "ok", "experiment": name, "variant": variant})
}

// ---- Rate limit handlers ----

func setRateLimitHandler(w http.ResponseWriter, r *http.Request) {
	var cfg RateLimitConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	rlMu.Lock()
	rateLimits[rateLimitScopeKey(cfg.Route, cfg.UserID, cfg.TenantID)] = cfg.Limit
	rlMu.Unlock()
	if store != nil {
		store.Set(r.Context(), "ratelimit:"+rateLimitScopeKey(cfg.Route, cfg.UserID, cfg.TenantID), fmt.Sprintf("%d", cfg.Limit))
	}
	jsonOK(w, cfg)
}

func getRateLimitHandler(w http.ResponseWriter, r *http.Request) {
	route := routeParam(r, "route")
	userID := r.URL.Query().Get("userId")
	tenantID := r.URL.Query().Get("tenantId")
	rlMu.RLock()
	limit, ok, matchedKey := resolveRateLimit(route, userID, tenantID, rateLimits)
	rlMu.RUnlock()
	jsonOK(w, map[string]interface{}{"route": route, "userId": userID, "tenantId": tenantID, "limit": limit, "configured": ok, "matchedScope": matchedKey})
}

func checkRateLimitHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Route    string `json:"route"`
		UserID   string `json:"userId"`
		TenantID string `json:"tenantId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	rlMu.RLock()
	limit, ok, _ := resolveRateLimit(req.Route, req.UserID, req.TenantID, rateLimits)
	rlMu.RUnlock()
	if !ok {
		limit = 100
	}
	allowed := Allow(req.Route, req.UserID, req.TenantID, limit)
	if !allowed {
		ThrottledRequests.WithLabelValues(req.Route).Inc()
	}
	jsonOK(w, map[string]interface{}{"allowed": allowed, "route": req.Route})
}

// ---- Circuit breaker handlers ----

func setCircuitBreakerHandler(w http.ResponseWriter, r *http.Request) {
	route := r.URL.Query().Get("route")
	state := r.URL.Query().Get("state")
	var req struct {
		Route              string  `json:"Route"`
		State              string  `json:"State"`
		ErrorThreshold     float64 `json:"ErrorThreshold"`
		LatencyThresholdMs int     `json:"LatencyThresholdMs"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if route == "" {
		route = req.Route
	}
	if state == "" {
		state = req.State
	}
	if route == "" {
		jsonError(w, "route is required", http.StatusBadRequest)
		return
	}
	cb := ensureCircuitBreaker(route)
	if state != "" {
		cb.SetState(state)
	}
	if req.ErrorThreshold > 0 {
		cb.mu.Lock()
		cb.ErrorThreshold = req.ErrorThreshold
		if req.LatencyThresholdMs > 0 {
			cb.LatencyThresholdMs = req.LatencyThresholdMs
		}
		cb.mu.Unlock()
	} else if req.LatencyThresholdMs > 0 {
		cb.mu.Lock()
		cb.LatencyThresholdMs = req.LatencyThresholdMs
		cb.mu.Unlock()
	}
	CircuitBreakerStateGauge.WithLabelValues(route).Set(stateToFloat(cb.GetState()))
	persistJSON(r.Context(), "cb:", route, cb)
	jsonOK(w, map[string]interface{}{
		"route":              cb.Route,
		"state":              cb.GetState(),
		"errorThreshold":     cb.ErrorThreshold,
		"latencyThresholdMs": cb.LatencyThresholdMs,
	})
}

func getCircuitBreakerHandler(w http.ResponseWriter, r *http.Request) {
	route := routeParam(r, "route")
	cbMu.RLock()
	cb, ok := circuitBreakers[route]
	cbMu.RUnlock()
	if !ok {
		jsonOK(w, map[string]interface{}{"route": route, "state": "closed"})
		return
	}
	jsonOK(w, map[string]interface{}{
		"route":              cb.Route,
		"state":              cb.GetState(),
		"errorThreshold":     cb.ErrorThreshold,
		"latencyMs":          cb.LatencyMs,
		"latencyThresholdMs": cb.LatencyThresholdMs,
	})
}

func checkCircuitBreakerHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Route string `json:"route"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			jsonError(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	route := strings.TrimSpace(req.Route)
	if route == "" {
		route = strings.TrimSpace(r.URL.Query().Get("route"))
	}
	if route == "" {
		jsonError(w, "route is required", http.StatusBadRequest)
		return
	}
	cb := ensureCircuitBreaker(route)
	allowed, state := cb.Check()
	CircuitBreakerStateGauge.WithLabelValues(route).Set(stateToFloat(state))
	persistJSON(r.Context(), "cb:", route, cb)
	jsonOK(w, map[string]interface{}{"route": route, "allowed": allowed, "state": state})
}

func reportCircuitBreakerHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Route     string `json:"route"`
		Success   bool   `json:"success"`
		LatencyMs int    `json:"latencyMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Route == "" {
		jsonError(w, "route is required", http.StatusBadRequest)
		return
	}
	cb := ensureCircuitBreaker(req.Route)
	cb.Observe(req.Success, req.LatencyMs)
	CircuitBreakerStateGauge.WithLabelValues(req.Route).Set(stateToFloat(cb.GetState()))
	persistJSON(r.Context(), "cb:", req.Route, cb)
	jsonOK(w, map[string]interface{}{
		"route":              cb.Route,
		"state":              cb.GetState(),
		"latencyMs":          req.LatencyMs,
		"latencyThresholdMs": cb.LatencyThresholdMs,
	})
}

func ensureCircuitBreaker(route string) *CircuitBreaker {
	cbMu.Lock()
	defer cbMu.Unlock()
	cb, ok := circuitBreakers[route]
	if !ok {
		cb = NewCircuitBreaker(route)
		circuitBreakers[route] = cb
	}
	return cb
}

func routeParam(r *http.Request, key string) string {
	if value := r.PathValue(key); value != "" {
		return value
	}
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	if r.URL.Path == "/ratelimit" || r.URL.Path == "/circuitbreaker" {
		return ""
	}
	return pathVar(r, key)
}

// ---- Generic config handlers ----

func setConfigHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if store == nil {
		jsonError(w, "config store not available", http.StatusServiceUnavailable)
		return
	}
	if err := store.Set(r.Context(), req.Key, req.Value); err != nil {
		jsonError(w, "store error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"key": req.Key, "value": req.Value})
}

func getConfigHandler(w http.ResponseWriter, r *http.Request) {
	key := pathVar(r, "key")
	if store == nil {
		jsonError(w, "config store not available", http.StatusServiceUnavailable)
		return
	}
	val, err := store.Get(r.Context(), key)
	if err != nil {
		jsonError(w, "key not found", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]string{"key": key, "value": val})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
}
