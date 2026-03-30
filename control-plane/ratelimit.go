package main

import (
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Token-bucket rate limiter
// ---------------------------------------------------------------------------

// RateLimitConfig holds the configuration for a route's rate limit.
type RateLimitConfig struct {
	Route    string `json:"route"`
	UserID   string `json:"userId,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
	Limit    int    `json:"limit"` // allowed requests per second
}

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
	rate       float64 // tokens added per second
	capacity   float64
	mu         sync.Mutex
}

var (
	buckets   = make(map[string]*tokenBucket)
	bucketsMu sync.Mutex
)

// Allow returns true if the request is within the rate limit for the given
// route/user/tenant combination. The bucket is created on first call.
func Allow(route, userID, tenantID string, limit int) bool {
	key := route + ":" + userID + ":" + tenantID

	bucketsMu.Lock()
	b, ok := buckets[key]
	if !ok {
		b = &tokenBucket{
			tokens:     float64(limit),
			lastRefill: time.Now(),
			rate:       float64(limit),
			capacity:   float64(limit),
		}
		buckets[key] = b
	}
	bucketsMu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.rate
	oldCapacity := b.capacity
	b.rate = float64(limit)
	b.capacity = float64(limit)
	if b.capacity > oldCapacity {
		b.tokens += b.capacity - oldCapacity
	}
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now

	if b.tokens >= 1.0 {
		b.tokens--
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Circuit breaker
// ---------------------------------------------------------------------------

// CircuitBreaker implements the three-state circuit breaker pattern.
// States: closed (normal), open (rejecting), half-open (probing).
type CircuitBreaker struct {
	Route              string  `json:"Route"`
	State              string  `json:"State"`          // closed | open | half-open
	ErrorThreshold     float64 `json:"ErrorThreshold"` // fraction, e.g. 0.5
	LatencyMs          int     `json:"LatencyMs,omitempty"`
	LatencyThresholdMs int     `json:"LatencyThresholdMs,omitempty"`
	LastTransitionUnix int64   `json:"LastTransitionUnix,omitempty"`
	successes          int
	failures           int
	lastChange         time.Time
	openDuration       time.Duration // how long to stay open before probing
	probeInFlight      bool
	mu                 sync.Mutex
}

// NewCircuitBreaker creates a circuit breaker with sensible defaults.
func NewCircuitBreaker(route string) *CircuitBreaker {
	cb := &CircuitBreaker{
		Route:              route,
		State:              "closed",
		ErrorThreshold:     0.5,
		LatencyThresholdMs: 250,
		openDuration:       10 * time.Second,
	}
	cb.setLastChange(time.Now())
	return cb
}

func rateLimitScopeKey(route, userID, tenantID string) string {
	if userID == "" && tenantID == "" {
		return route
	}
	return route + "|" + userID + "|" + tenantID
}

func resolveRateLimit(route, userID, tenantID string, limits map[string]int) (int, bool, string) {
	keys := []string{
		rateLimitScopeKey(route, userID, tenantID),
		rateLimitScopeKey(route, userID, ""),
		rateLimitScopeKey(route, "", tenantID),
		rateLimitScopeKey(route, "", ""),
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if limit, ok := limits[key]; ok {
			return limit, true, key
		}
	}
	return 0, false, ""
}

func (cb *CircuitBreaker) restoreRuntimeState(now time.Time) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.openDuration = 10 * time.Second
	if cb.LastTransitionUnix > 0 {
		cb.lastChange = time.Unix(0, cb.LastTransitionUnix)
	} else {
		cb.setLastChangeLocked(now)
	}
	cb.probeInFlight = false
}

func (cb *CircuitBreaker) refreshStateLocked(now time.Time) {
	if cb.State == "open" && now.Sub(cb.lastChange) >= cb.openDuration {
		cb.State = "half-open"
		cb.probeInFlight = false
		cb.setLastChangeLocked(now)
	}
}

func (cb *CircuitBreaker) setLastChange(now time.Time) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.setLastChangeLocked(now)
}

func (cb *CircuitBreaker) setLastChangeLocked(now time.Time) {
	cb.lastChange = now
	cb.LastTransitionUnix = now.UnixNano()
}

// Check returns whether the request should proceed and the current breaker state.
func (cb *CircuitBreaker) Check() (bool, string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.refreshStateLocked(time.Now())
	switch cb.State {
	case "open":
		return false, cb.State
	case "half-open":
		if cb.probeInFlight {
			return false, cb.State
		}
		cb.probeInFlight = true
		return true, cb.State
	default:
		return true, cb.State
	}
}

// RecordSuccess adjusts counters; transitions half-open → closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.successes++
	cb.LatencyMs = 0
	cb.probeInFlight = false
	if cb.State == "half-open" {
		cb.State = "closed"
		cb.successes = 0
		cb.failures = 0
		cb.setLastChangeLocked(time.Now())
	}
}

// RecordFailure adjusts counters; transitions to open when error rate exceeds threshold.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.probeInFlight = false
	total := cb.successes + cb.failures
	if total > 0 && float64(cb.failures)/float64(total) >= cb.ErrorThreshold {
		cb.State = "open"
		cb.successes = 0
		cb.failures = 0
		cb.setLastChangeLocked(time.Now())
	}
}

// Observe records request outcome and latency against the breaker thresholds.
func (cb *CircuitBreaker) Observe(success bool, latencyMs int) {
	cb.mu.Lock()
	cb.LatencyMs = latencyMs
	latencyExceeded := cb.LatencyThresholdMs > 0 && latencyMs > cb.LatencyThresholdMs
	cb.mu.Unlock()

	if success && !latencyExceeded {
		cb.RecordSuccess()
		return
	}
	cb.RecordFailure()
}

// GetState returns the current state string without holding the lock long.
func (cb *CircuitBreaker) GetState() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.State
}

// SetState forcibly sets the circuit breaker state (used by the admin API).
func (cb *CircuitBreaker) SetState(state string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.State = state
	cb.successes = 0
	cb.failures = 0
	cb.probeInFlight = false
	cb.setLastChangeLocked(time.Now())
}

// stateToFloat converts state label to a numeric gauge value.
func stateToFloat(state string) float64 {
	switch state {
	case "open":
		return 1.0
	case "half-open":
		return 0.5
	default:
		return 0.0
	}
}
