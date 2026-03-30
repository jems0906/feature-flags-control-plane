package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreateFlagHandler(t *testing.T) {
	req := httptest.NewRequest("POST", "/flags", nil)
	rw := httptest.NewRecorder()
	createFlagHandler(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
}

func TestGetFlagHandler(t *testing.T) {
	flags = make(map[string]*FeatureFlag)
	req := httptest.NewRequest("GET", "/flags/test", nil)
	req.SetPathValue("name", "test")
	rw := httptest.NewRecorder()
	getFlagHandler(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rw.Code)
	}
}

func TestSetCircuitBreakerHandler(t *testing.T) {
	req := httptest.NewRequest("POST", "/circuitbreaker?route=test&state=closed", nil)
	rw := httptest.NewRecorder()
	setCircuitBreakerHandler(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
}

func TestGetCircuitBreakerHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/circuitbreaker/test", nil)
	rw := httptest.NewRecorder()
	getCircuitBreakerHandler(rw, req)
	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
}

func TestReportCircuitBreakerHandlerTripsOnFailure(t *testing.T) {
	circuitBreakers = make(map[string]*CircuitBreaker)
	req := httptest.NewRequest("POST", "/circuitbreaker/report", strings.NewReader(`{"route":"/demo/action","success":false,"latencyMs":500}`))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	reportCircuitBreakerHandler(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	cb := ensureCircuitBreaker("/demo/action")
	if got := cb.GetState(); got != "open" {
		t.Fatalf("expected open, got %s", got)
	}
}

func TestGetCircuitBreakerHandlerSupportsQueryRoute(t *testing.T) {
	circuitBreakers = make(map[string]*CircuitBreaker)
	cb := ensureCircuitBreaker("/demo/action")
	cb.SetState("half-open")

	req := httptest.NewRequest("GET", "/circuitbreaker?route=%2Fdemo%2Faction", nil)
	rw := httptest.NewRecorder()
	getCircuitBreakerHandler(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rw.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := body["state"].(string); got != "half-open" {
		t.Fatalf("expected half-open, got %q", got)
	}
}

func TestCheckCircuitBreakerHandlerAllowsSingleProbeInHalfOpen(t *testing.T) {
	circuitBreakers = make(map[string]*CircuitBreaker)
	cb := ensureCircuitBreaker("/demo/action")
	cb.SetState("open")
	cb.setLastChange(time.Now().Add(-11 * time.Second))

	firstReq := httptest.NewRequest(http.MethodPost, "/circuitbreaker/check", strings.NewReader(`{"route":"/demo/action"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstRw := httptest.NewRecorder()
	checkCircuitBreakerHandler(firstRw, firstReq)
	if firstRw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", firstRw.Code)
	}
	var first map[string]interface{}
	if err := json.NewDecoder(firstRw.Body).Decode(&first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if allowed, _ := first["allowed"].(bool); !allowed {
		t.Fatal("expected first half-open probe to be allowed")
	}
	if state, _ := first["state"].(string); state != "half-open" {
		t.Fatalf("expected half-open state, got %q", state)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/circuitbreaker/check", strings.NewReader(`{"route":"/demo/action"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondRw := httptest.NewRecorder()
	checkCircuitBreakerHandler(secondRw, secondReq)
	var second map[string]interface{}
	if err := json.NewDecoder(secondRw.Body).Decode(&second); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if allowed, _ := second["allowed"].(bool); allowed {
		t.Fatal("expected concurrent half-open probe to be rejected")
	}
	if state, _ := second["state"].(string); state != "half-open" {
		t.Fatalf("expected half-open state, got %q", state)
	}

	cb.Observe(true, 20)
	if got := cb.GetState(); got != "closed" {
		t.Fatalf("expected closed after successful probe, got %s", got)
	}
	allowed, state := cb.Check()
	if !allowed || state != "closed" {
		t.Fatalf("expected closed breaker to allow traffic, got allowed=%v state=%s", allowed, state)
	}
}

func TestAuthMiddlewareRejectsProtectedWriteWithoutToken(t *testing.T) {
	t.Setenv("CONTROL_PLANE_AUTH_TOKEN", "secret-token")
	handler := authMiddleware(http.HandlerFunc(createFlagHandler))
	req := httptest.NewRequest(http.MethodPost, "/flags", nil)
	rw := httptest.NewRecorder()

	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestAuthMiddlewareAllowsProtectedWriteWithBearerToken(t *testing.T) {
	t.Setenv("CONTROL_PLANE_AUTH_TOKEN", "secret-token")
	handler := authMiddleware(http.HandlerFunc(createFlagHandler))
	req := httptest.NewRequest(http.MethodPost, "/flags", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rw := httptest.NewRecorder()

	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestAuthMiddlewareLeavesRuntimeCheckOpen(t *testing.T) {
	t.Setenv("CONTROL_PLANE_AUTH_TOKEN", "secret-token")
	handler := authMiddleware(http.HandlerFunc(checkRateLimitHandler))
	req := httptest.NewRequest(http.MethodPost, "/ratelimit/check", strings.NewReader(`{"route":"/demo/action"}`))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()

	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestAuthMiddlewareLeavesCircuitBreakerCheckOpen(t *testing.T) {
	t.Setenv("CONTROL_PLANE_AUTH_TOKEN", "secret-token")
	handler := authMiddleware(http.HandlerFunc(checkCircuitBreakerHandler))
	req := httptest.NewRequest(http.MethodPost, "/circuitbreaker/check", strings.NewReader(`{"route":"/demo/action"}`))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()

	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

// ---------------------------------------------------------------------------
// FeatureFlag.IsFlagEnabled – targeting rule coverage
// ---------------------------------------------------------------------------

func TestIsFlagEnabled_Disabled(t *testing.T) {
	f := &FeatureFlag{Name: "f", Enabled: false}
	if f.IsFlagEnabled(EvalContext{UserID: "alice"}) {
		t.Fatal("disabled flag should not be enabled for any user")
	}
}

func TestIsFlagEnabled_NoRules(t *testing.T) {
	f := &FeatureFlag{Name: "f", Enabled: true}
	if !f.IsFlagEnabled(EvalContext{UserID: "alice"}) {
		t.Fatal("flag with no rules should be enabled for everyone")
	}
}

func TestIsFlagEnabled_UserRule_Match(t *testing.T) {
	f := &FeatureFlag{
		Name:        "f",
		Enabled:     true,
		TargetRules: []TargetRule{{Type: "user", Value: "alice"}},
	}
	if !f.IsFlagEnabled(EvalContext{UserID: "alice"}) {
		t.Fatal("user rule should match alice")
	}
	if f.IsFlagEnabled(EvalContext{UserID: "bob"}) {
		t.Fatal("user rule should not match bob")
	}
}

func TestIsFlagEnabled_TenantRule_Match(t *testing.T) {
	f := &FeatureFlag{
		Name:        "f",
		Enabled:     true,
		TargetRules: []TargetRule{{Type: "tenant", Value: "acme"}},
	}
	if !f.IsFlagEnabled(EvalContext{TenantID: "acme"}) {
		t.Fatal("tenant rule should match acme")
	}
	if f.IsFlagEnabled(EvalContext{TenantID: "other"}) {
		t.Fatal("tenant rule should not match other tenant")
	}
}

func TestIsFlagEnabled_HeaderRule_Match(t *testing.T) {
	f := &FeatureFlag{
		Name:        "f",
		Enabled:     true,
		TargetRules: []TargetRule{{Type: "header", Value: "x-beta"}},
	}
	if !f.IsFlagEnabled(EvalContext{Headers: map[string]string{"x-beta": "1"}}) {
		t.Fatal("header rule should match when header present and non-empty")
	}
	if f.IsFlagEnabled(EvalContext{Headers: map[string]string{}}) {
		t.Fatal("header rule should not match when header absent")
	}
}

func TestIsFlagEnabled_PercentageRule_Deterministic(t *testing.T) {
	f := &FeatureFlag{
		Name:        "f",
		Enabled:     true,
		TargetRules: []TargetRule{{Type: "percentage", Rollout: 100}},
	}
	// 100% rollout — every user gets it.
	for _, uid := range []string{"alice", "bob", "charlie", "dave"} {
		if !f.IsFlagEnabled(EvalContext{UserID: uid}) {
			t.Fatalf("100%% rollout flag should be enabled for %s", uid)
		}
	}

	f.TargetRules[0].Rollout = 0
	for _, uid := range []string{"alice", "bob", "charlie"} {
		if f.IsFlagEnabled(EvalContext{UserID: uid}) {
			t.Fatalf("0%% rollout flag should not be enabled for %s", uid)
		}
	}
}

func TestIsFlagEnabled_PercentageRule_SameUserSameResult(t *testing.T) {
	f := &FeatureFlag{
		Name:        "f",
		Enabled:     true,
		TargetRules: []TargetRule{{Type: "percentage", Rollout: 50}},
	}
	// Same user must always get the same result (deterministic hashing).
	first := f.IsFlagEnabled(EvalContext{UserID: "alice"})
	for i := 0; i < 10; i++ {
		if f.IsFlagEnabled(EvalContext{UserID: "alice"}) != first {
			t.Fatal("percentage rollout must be deterministic for the same user")
		}
	}
}

// ---------------------------------------------------------------------------
// A/B experiment variant assignment
// ---------------------------------------------------------------------------

func TestAssignVariant_Deterministic(t *testing.T) {
	e := &Experiment{Name: "btn", Variants: []string{"A", "B"}}
	v1 := e.AssignVariant("alice")
	if v1 == "" {
		t.Fatal("expected a non-empty variant")
	}
	// Must be stable across repeated calls.
	for i := 0; i < 10; i++ {
		if e.AssignVariant("alice") != v1 {
			t.Fatal("variant must be deterministic for the same user")
		}
	}
}

func TestAssignVariant_DistributionBothVariantsCovered(t *testing.T) {
	e := &Experiment{Name: "btn", Variants: []string{"A", "B"}}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		seen[e.AssignVariant(fmt.Sprintf("user%d", i))] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("expected both variants to appear in 50 users, got %v", seen)
	}
}

func TestAssignVariant_EmptyVariants(t *testing.T) {
	e := &Experiment{Name: "empty", Variants: nil}
	if got := e.AssignVariant("alice"); got != "" {
		t.Fatalf("expected empty string for experiment with no variants, got %q", got)
	}
}

func TestAssignVariant_DifferentExperimentsIndependent(t *testing.T) {
	e1 := &Experiment{Name: "exp1", Variants: []string{"A", "B"}}
	e2 := &Experiment{Name: "exp2", Variants: []string{"A", "B"}}
	// Two experiments with the same user should not always agree (hash mixes in name).
	// With just two experiments this may rarely collide, so test several users.
	alwaysSame := true
	for i := 0; i < 20; i++ {
		uid := fmt.Sprintf("u%d", i)
		if e1.AssignVariant(uid) != e2.AssignVariant(uid) {
			alwaysSame = false
			break
		}
	}
	if alwaysSame {
		t.Fatal("experiments with different names should not always assign the same variant")
	}
}

// ---------------------------------------------------------------------------
// Token-bucket rate limiter
// ---------------------------------------------------------------------------

func TestAllow_WithinLimit(t *testing.T) {
	// Fresh bucket with limit 5; first 5 calls must succeed.
	route := "test-allow-within"
	for i := 0; i < 5; i++ {
		if !Allow(route, "user1", "", 5) {
			t.Fatalf("call %d should be allowed within limit", i+1)
		}
	}
}

func TestAllow_ExceedsLimit(t *testing.T) {
	// Fresh bucket with limit 2; third call must be rejected.
	route := "test-allow-exceed"
	Allow(route, "u", "", 2)
	Allow(route, "u", "", 2)
	if Allow(route, "u", "", 2) {
		t.Fatal("third call should be rejected when limit is 2")
	}
}

func TestAllow_PerRouteIsolation(t *testing.T) {
	// Two routes share no bucket state.
	routeA := "test-isolate-a"
	routeB := "test-isolate-b"
	for i := 0; i < 3; i++ {
		Allow(routeA, "u", "", 3)
	}
	// routeB must still have a full bucket.
	if !Allow(routeB, "u", "", 3) {
		t.Fatal("routeB bucket should be independent of routeA")
	}
}

func TestAllow_UpdatesExistingBucketLimit(t *testing.T) {
	route := "test-limit-update"
	if !Allow(route, "u", "", 1) {
		t.Fatal("first request should be allowed")
	}
	if Allow(route, "u", "", 1) {
		t.Fatal("second request should be throttled at limit 1")
	}
	if !Allow(route, "u", "", 3) {
		t.Fatal("existing bucket should adopt updated limit")
	}
}

func TestResolveRateLimitPrefersSpecificScope(t *testing.T) {
	limits := map[string]int{
		rateLimitScopeKey("/api", "", ""):           100,
		rateLimitScopeKey("/api", "alice", ""):      25,
		rateLimitScopeKey("/api", "", "tenant-a"):   40,
		rateLimitScopeKey("/api", "alice", "tenant-a"): 10,
	}

	if limit, ok, _ := resolveRateLimit("/api", "alice", "tenant-a", limits); !ok || limit != 10 {
		t.Fatalf("expected exact override, got ok=%v limit=%d", ok, limit)
	}
	if limit, ok, _ := resolveRateLimit("/api", "alice", "tenant-b", limits); !ok || limit != 25 {
		t.Fatalf("expected user override, got ok=%v limit=%d", ok, limit)
	}
	if limit, ok, _ := resolveRateLimit("/api", "bob", "tenant-a", limits); !ok || limit != 40 {
		t.Fatalf("expected tenant override, got ok=%v limit=%d", ok, limit)
	}
	if limit, ok, _ := resolveRateLimit("/api", "bob", "tenant-b", limits); !ok || limit != 100 {
		t.Fatalf("expected global route limit, got ok=%v limit=%d", ok, limit)
	}
}

// ---------------------------------------------------------------------------
// Circuit breaker state transitions
// ---------------------------------------------------------------------------

func TestCircuitBreaker_OpenOnHighErrorRate(t *testing.T) {
	cb := NewCircuitBreaker("/test")
	cb.ErrorThreshold = 0.5
	// 2 failures → error rate 100% ≥ 50% → open
	cb.Observe(false, 10)
	cb.Observe(false, 10)
	if cb.GetState() != "open" {
		t.Fatalf("expected open, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_ClosedAfterSuccess(t *testing.T) {
	cb := NewCircuitBreaker("/test")
	cb.SetState("half-open")
	cb.Observe(true, 10)
	if cb.GetState() != "closed" {
		t.Fatalf("expected closed after success in half-open, got %s", cb.GetState())
	}
}

func TestCircuitBreaker_OpenOnHighLatency(t *testing.T) {
	cb := NewCircuitBreaker("/test")
	cb.LatencyThresholdMs = 100
	cb.ErrorThreshold = 0.5
	// latency exceeds threshold even on "success" → treated as failure
	cb.Observe(true, 200)
	cb.Observe(true, 200)
	if cb.GetState() != "open" {
		t.Fatalf("expected open on latency breach, got %s", cb.GetState())
	}
}

func TestLoadFromStorePreservesCircuitBreakerTransitionTime(t *testing.T) {
	store = NewConfigStore("")
	old := NewCircuitBreaker("/demo/action")
	old.SetState("open")
	old.setLastChange(time.Now().Add(-11 * time.Second))
	persistJSON(context.Background(), "cb:", old.Route, old)

	loadFromStore(context.Background())
	cb := ensureCircuitBreaker("/demo/action")
	allowed, state := cb.Check()
	if !allowed || state != "half-open" {
		t.Fatalf("expected persisted open breaker to become half-open probe, got allowed=%v state=%s", allowed, state)
	}
	store = nil
	flags = make(map[string]*FeatureFlag)
	experiments = make(map[string]*Experiment)
	circuitBreakers = make(map[string]*CircuitBreaker)
	rateLimits = make(map[string]int)
}

// ---------------------------------------------------------------------------
// Evaluate flag HTTP handler – end-to-end
// ---------------------------------------------------------------------------

func TestEvaluateFlagHandler(t *testing.T) {
	flags = make(map[string]*FeatureFlag)
	flags["beta"] = &FeatureFlag{
		Name:    "beta",
		Enabled: true,
		TargetRules: []TargetRule{
			{Type: "user", Value: "alice"},
		},
	}

	body := `{"UserID":"alice"}`
	req := httptest.NewRequest(http.MethodPost, "/flags/beta/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Inject path value so pathVar works.
	req.SetPathValue("name", "beta")
	rw := httptest.NewRecorder()
	evaluateFlagHandler(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rw.Code, rw.Body.String())
	}
	var result map[string]interface{}
	if err := json.NewDecoder(rw.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if enabled, _ := result["enabled"].(bool); !enabled {
		t.Fatal("expected flag to be enabled for alice")
	}
}

func TestEvaluateFlagHandler_NotFound(t *testing.T) {
	flags = make(map[string]*FeatureFlag)
	req := httptest.NewRequest(http.MethodPost, "/flags/nonexistent/evaluate", strings.NewReader(`{}`))
	req.SetPathValue("name", "nonexistent")
	rw := httptest.NewRecorder()
	evaluateFlagHandler(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rw.Code)
	}
}

// ---------------------------------------------------------------------------
// Rate limit HTTP handler
// ---------------------------------------------------------------------------

func TestCheckRateLimitHandler_Allowed(t *testing.T) {
	rateLimits = map[string]int{"/api": 100}
	body := `{"route":"/api","userId":"u1"}`
	req := httptest.NewRequest(http.MethodPost, "/ratelimit/check", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	checkRateLimitHandler(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(rw.Body).Decode(&resp)
	if allowed, _ := resp["allowed"].(bool); !allowed {
		t.Fatal("expected allowed=true")
	}
}

// ---------------------------------------------------------------------------
// Variant handler
// ---------------------------------------------------------------------------

func TestGetVariantHandler(t *testing.T) {
	experiments = map[string]*Experiment{
		"test-exp": {Name: "test-exp", Variants: []string{"control", "treatment"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/experiment/test-exp/variant?userId=alice", nil)
	req.SetPathValue("name", "test-exp")
	rw := httptest.NewRecorder()
	getVariantHandler(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	var resp map[string]string
	json.NewDecoder(rw.Body).Decode(&resp)
	if resp["variant"] != "control" && resp["variant"] != "treatment" {
		t.Fatalf("unexpected variant: %q", resp["variant"])
	}
}

func TestGetVariantHandler_MissingUserId(t *testing.T) {
	experiments = map[string]*Experiment{
		"e": {Name: "e", Variants: []string{"A", "B"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/experiment/e/variant", nil)
	req.SetPathValue("name", "e")
	rw := httptest.NewRecorder()
	getVariantHandler(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rw.Code)
	}
}
