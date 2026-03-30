package main

// Minimal Prometheus-compatible metrics (no external deps).
// Supports CounterVec, GaugeVec, and HistogramVec; outputs standard text
// format at GET /metrics.

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

var allMetrics []prometheusWriter // populated by newCounter / newGauge / newHistogram

type prometheusWriter interface {
	writePrometheus(sb *strings.Builder)
}

// ServeMetrics is the /metrics HTTP handler.
func ServeMetrics(w http.ResponseWriter, _ *http.Request) {
	var sb strings.Builder
	for _, m := range allMetrics {
		m.writePrometheus(&sb)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprint(w, sb.String())
}

// ---------------------------------------------------------------------------
// CounterVec
// ---------------------------------------------------------------------------

type counterEntry struct {
	vals []string
	n    atomic.Int64
}

// CounterVec is a set of monotonically increasing counters partitioned by
// label values.
type CounterVec struct {
	name   string
	help   string
	labels []string
	m      sync.Map // labelKey → *counterEntry
}

func newCounter(name, help string, labels ...string) *CounterVec {
	cv := &CounterVec{name: name, help: help, labels: labels}
	allMetrics = append(allMetrics, cv)
	return cv
}

func (cv *CounterVec) WithLabelValues(vals ...string) *labelCounter {
	key := labelKey(vals)
	e, _ := cv.m.LoadOrStore(key, &counterEntry{vals: cloneSlice(vals)})
	return &labelCounter{e: e.(*counterEntry)}
}

type labelCounter struct{ e *counterEntry }

func (lc *labelCounter) Inc() { lc.e.n.Add(1) }

func (cv *CounterVec) writePrometheus(sb *strings.Builder) {
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s counter\n", cv.name, cv.help, cv.name)
	cv.m.Range(func(_, v any) bool {
		e := v.(*counterEntry)
		fmt.Fprintf(sb, "%s%s %d\n", cv.name, labelStr(cv.labels, e.vals), e.n.Load())
		return true
	})
}

// ---------------------------------------------------------------------------
// GaugeVec
// ---------------------------------------------------------------------------

type gaugeEntry struct {
	vals []string
	mu   sync.Mutex
	v    float64
}

// GaugeVec is a set of float64 gauges partitioned by label values.
type GaugeVec struct {
	name   string
	help   string
	labels []string
	m      sync.Map // labelKey → *gaugeEntry
}

func newGauge(name, help string, labels ...string) *GaugeVec {
	gv := &GaugeVec{name: name, help: help, labels: labels}
	allMetrics = append(allMetrics, gv)
	return gv
}

func (gv *GaugeVec) WithLabelValues(vals ...string) *labelGauge {
	key := labelKey(vals)
	e, _ := gv.m.LoadOrStore(key, &gaugeEntry{vals: cloneSlice(vals)})
	return &labelGauge{e: e.(*gaugeEntry)}
}

type labelGauge struct{ e *gaugeEntry }

func (lg *labelGauge) Set(v float64) {
	lg.e.mu.Lock()
	lg.e.v = v
	lg.e.mu.Unlock()
}

func (gv *GaugeVec) writePrometheus(sb *strings.Builder) {
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s gauge\n", gv.name, gv.help, gv.name)
	gv.m.Range(func(_, v any) bool {
		e := v.(*gaugeEntry)
		e.mu.Lock()
		val := e.v
		e.mu.Unlock()
		fmt.Fprintf(sb, "%s%s %g\n", gv.name, labelStr(gv.labels, e.vals), val)
		return true
	})
}

// ---------------------------------------------------------------------------
// HistogramVec
// ---------------------------------------------------------------------------

type histEntry struct {
	vals    []string
	mu      sync.Mutex
	counts  []int64 // per bucket (index = bucket upper bound; last = +Inf)
	sum     float64
	total   int64
	buckets []float64
}

// HistogramVec is a set of histograms with configurable bucket boundaries.
type HistogramVec struct {
	name    string
	help    string
	labels  []string
	buckets []float64
	m       sync.Map // labelKey → *histEntry
}

func newHistogram(name, help string, buckets []float64, labels ...string) *HistogramVec {
	hv := &HistogramVec{name: name, help: help, buckets: buckets, labels: labels}
	allMetrics = append(allMetrics, hv)
	return hv
}

func (hv *HistogramVec) WithLabelValues(vals ...string) *labelHistogram {
	key := labelKey(vals)
	e, _ := hv.m.LoadOrStore(key, &histEntry{
		vals:    cloneSlice(vals),
		counts:  make([]int64, len(hv.buckets)+1),
		buckets: hv.buckets,
	})
	return &labelHistogram{e: e.(*histEntry)}
}

type labelHistogram struct{ e *histEntry }

func (lh *labelHistogram) Observe(v float64) {
	e := lh.e
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sum += v
	e.total++
	for i, b := range e.buckets {
		if v <= b {
			e.counts[i]++
		}
	}
	e.counts[len(e.buckets)]++ // +Inf bucket always increments
}

func (hv *HistogramVec) writePrometheus(sb *strings.Builder) {
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s histogram\n", hv.name, hv.help, hv.name)
	hv.m.Range(func(_, v any) bool {
		e := v.(*histEntry)
		e.mu.Lock()
		base := labelStr(hv.labels, e.vals)
		for i, b := range e.buckets {
			fmt.Fprintf(sb, "%s_bucket%s %d\n", hv.name, insertLabel(base, "le", fmt.Sprintf("%g", b)), e.counts[i])
		}
		fmt.Fprintf(sb, "%s_bucket%s %d\n", hv.name, insertLabel(base, "le", "+Inf"), e.counts[len(e.buckets)])
		fmt.Fprintf(sb, "%s_sum%s %g\n", hv.name, base, e.sum)
		fmt.Fprintf(sb, "%s_count%s %d\n", hv.name, base, e.total)
		e.mu.Unlock()
		return true
	})
}

// ---------------------------------------------------------------------------
// Package-level metric variables (self-register via constructor calls)
// ---------------------------------------------------------------------------

var (
	QPS = newCounter(
		"control_plane_requests_total",
		"Total requests handled, labelled by route.",
		"route",
	)
	ErrorRate = newCounter(
		"control_plane_errors_total",
		"Total HTTP errors (4xx/5xx), labelled by route.",
		"route",
	)
	Latency = newHistogram(
		"control_plane_latency_ms",
		"Request latency in milliseconds.",
		[]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500},
		"route",
	)
	VariantExposure = newCounter(
		"experiment_variant_exposure_total",
		"Number of times each variant was exposed to a user.",
		"experiment", "variant",
	)
	ConversionEvents = newCounter(
		"experiment_conversion_total",
		"Conversion events per experiment and variant.",
		"experiment", "variant",
	)
	FlagEvaluations = newCounter(
		"feature_flag_evaluations_total",
		"Feature flag evaluation results, labelled by flag name and result.",
		"flag", "result",
	)
	ThrottledRequests = newCounter(
		"rate_limit_throttled_total",
		"Total requests rejected by the rate limiter, labelled by route.",
		"route",
	)
	CircuitBreakerStateGauge = newGauge(
		"circuit_breaker_state",
		"Current circuit breaker state per route (0=closed, 0.5=half-open, 1=open).",
		"route",
	)
)

// InitMetrics is a no-op: all metrics register themselves at variable
// initialisation time via newCounter / newGauge / newHistogram.
func InitMetrics() {}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func labelKey(vals []string) string { return strings.Join(vals, "\x00") }

func cloneSlice(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// labelStr returns `{k1="v1",k2="v2"}` or "" when there are no labels.
func labelStr(names, vals []string) string {
	if len(names) == 0 {
		return ""
	}
	parts := make([]string, len(names))
	for i := range names {
		parts[i] = names[i] + `="` + escapeLabel(vals[i]) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// insertLabel appends an extra le="x" label inside an existing label set.
func insertLabel(existing, k, v string) string {
	kv := k + `="` + v + `"`
	if existing == "" {
		return "{" + kv + "}"
	}
	return existing[:len(existing)-1] + "," + kv + "}"
}

func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
