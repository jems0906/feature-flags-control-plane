package sdk

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// SDKClient – connects to the control-plane and provides typed helpers.
// ---------------------------------------------------------------------------

// SDKClient is a thread-safe client for the control-plane API.
// It supports polling and SSE for hot-reload of flags.
type SDKClient struct {
	ConfigURL    string
	AuthToken    string
	PollInterval time.Duration

	mu         sync.RWMutex
	lastConfig []byte
	flags      []map[string]interface{}
	onUpdate   func(flags []map[string]interface{})
}

// NewSDKClient creates a new client.
//   - configURL: base URL of the control-plane, e.g. "http://localhost:8080"
//   - pollInterval: how often to poll for config changes (0 to disable polling)
func NewSDKClient(configURL string, pollInterval time.Duration) *SDKClient {
	return &SDKClient{
		ConfigURL:    strings.TrimRight(configURL, "/"),
		PollInterval: pollInterval,
	}
}

// OnUpdate registers a callback that fires whenever the flags payload changes.
func (c *SDKClient) OnUpdate(fn func(flags []map[string]interface{})) {
	c.mu.Lock()
	c.onUpdate = fn
	c.mu.Unlock()
}

// SetAuthToken configures the bearer token used for protected control-plane writes.
func (c *SDKClient) SetAuthToken(token string) {
	c.mu.Lock()
	c.AuthToken = strings.TrimSpace(token)
	c.mu.Unlock()
}

func (c *SDKClient) authToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AuthToken
}

func (c *SDKClient) newRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if token := c.authToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// ---------------------------------------------------------------------------
// Polling hot-reload
// ---------------------------------------------------------------------------

// StartPolling begins background polling for flag changes.
func (c *SDKClient) StartPolling(env string) {
	go func() {
		for {
			c.fetchFlags(env)
			time.Sleep(c.PollInterval)
		}
	}()
}

func (c *SDKClient) fetchFlags(env string) {
	url := fmt.Sprintf("%s/flags/all?env=%s", c.ConfigURL, env)
	resp, err := http.Get(url) //nolint:gosec // URL is supplied by operator, not end-users
	if err != nil {
		log.Printf("sdk poll: %v", err)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("sdk poll read: %v", err)
		return
	}

	c.mu.Lock()
	changed := string(body) != string(c.lastConfig)
	if changed {
		c.lastConfig = body
		var flags []map[string]interface{}
		if err := json.Unmarshal(body, &flags); err == nil {
			c.flags = flags
		}
	}
	cb := c.onUpdate
	flags := c.flags
	c.mu.Unlock()

	if changed && cb != nil {
		cb(flags)
	}
}

// ---------------------------------------------------------------------------
// SSE hot-reload
// ---------------------------------------------------------------------------

// StartSSE subscribes to the control-plane SSE stream for real-time updates.
// It reconnects automatically on error. Blocks – run in a goroutine.
func (c *SDKClient) StartSSE(env string) {
	log.Printf("sdk: starting SSE for env=%s", env)
	for {
		if err := c.connectSSE(env); err != nil {
			log.Printf("sdk SSE error: %v – reconnecting in 5s", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func (c *SDKClient) connectSSE(env string) error {
	url := fmt.Sprintf("%s/flags/stream?env=%s", c.ConfigURL, env)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status %s", resp.Status)
	}

	reader := bufio.NewReader(resp.Body)
	var eventType, eventData string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			eventData = strings.TrimSpace(line[5:])
		case line == "":
			if eventType == "update" {
				c.mu.Lock()
				c.lastConfig = []byte(eventData)
				c.mu.Unlock()
				log.Printf("sdk SSE update received")
				// Re-fetch full flag list on any update signal.
				c.fetchFlags(env)
			}
			eventType, eventData = "", ""
		}
	}
}

// ---------------------------------------------------------------------------
// Feature flag helpers
// ---------------------------------------------------------------------------

// IsEnabled evaluates a named flag for the given user/tenant context via the
// control-plane /flags/{name}/evaluate endpoint.
func (c *SDKClient) IsEnabled(flagName string, ctx map[string]string) (bool, error) {
	evalCtx := map[string]interface{}{
		"UserID":   ctx["userId"],
		"TenantID": ctx["tenantId"],
		"Headers":  ctx,
	}
	body, _ := json.Marshal(evalCtx)
	url := fmt.Sprintf("%s/flags/%s/evaluate", c.ConfigURL, flagName)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec
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

// ---------------------------------------------------------------------------
// Experiment helpers
// ---------------------------------------------------------------------------

// GetVariant returns the assigned A/B variant for a user in an experiment.
func (c *SDKClient) GetVariant(experimentName, userID string) (string, error) {
	url := fmt.Sprintf("%s/experiment/%s/variant?userId=%s", c.ConfigURL, experimentName, userID)
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

// RecordConversion records a conversion event for the given experiment and variant.
func (c *SDKClient) RecordConversion(experimentName, variant string) error {
	url := fmt.Sprintf("%s/experiment/%s/convert?variant=%s", c.ConfigURL, experimentName, variant)
	req, err := c.newRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---------------------------------------------------------------------------
// Rate limit helper
// ---------------------------------------------------------------------------

// CheckRateLimit asks the control-plane whether a request is within limits.
func (c *SDKClient) CheckRateLimit(route, userID, tenantID string) (bool, error) {
	payload := map[string]string{
		"route":    route,
		"userId":   userID,
		"tenantId": tenantID,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(c.ConfigURL+"/ratelimit/check", "application/json", bytes.NewReader(body)) //nolint:gosec
	if err != nil {
		return true, err // fail open on network error
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return true, err
	}
	allowed, _ := result["allowed"].(bool)
	return allowed, nil
}

// ---------------------------------------------------------------------------
// Circuit breaker helpers
// ---------------------------------------------------------------------------

// GetCircuitBreakerState returns the current state of the circuit breaker for
// the given route ("closed", "open", or "half-open"). Fails open: returns
// "closed" on any network or decode error so callers can proceed normally.
func (c *SDKClient) GetCircuitBreakerState(route string) (string, error) {
	url := fmt.Sprintf("%s/circuitbreaker?route=%s", c.ConfigURL, route)
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return "closed", err
	}
	defer resp.Body.Close()
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "closed", err
	}
	if state, ok := result["state"]; ok {
		return state, nil
	}
	return "closed", nil
}

// CheckCircuitBreaker returns whether the route is currently allowed to proceed
// and the current state of the breaker.
func (c *SDKClient) CheckCircuitBreaker(route string) (bool, string, error) {
	payload := map[string]interface{}{"route": route}
	body, _ := json.Marshal(payload)
	req, err := c.newRequest(http.MethodPost, c.ConfigURL+"/circuitbreaker/check", bytes.NewReader(body))
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

// ReportCircuitBreakerResult reports a completed request's outcome and
// latency to the control-plane so it can update circuit breaker state.
func (c *SDKClient) ReportCircuitBreakerResult(route string, success bool, latencyMs int) error {
	payload := map[string]interface{}{
		"route":     route,
		"success":   success,
		"latencyMs": latencyMs,
	}
	body, _ := json.Marshal(payload)
	req, err := c.newRequest(http.MethodPost, c.ConfigURL+"/circuitbreaker/report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
