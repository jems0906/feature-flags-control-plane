package main

// Minimal OpenTelemetry-compatible tracer (no external SDK).
// Spans are logged to stdout in a structured format that matches what
// OTel stdout exporter would produce, making it easy to swap for the
// real SDK when `go.opentelemetry.io/otel/sdk` becomes available.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"time"
)

// ---------------------------------------------------------------------------
// Tracer
// ---------------------------------------------------------------------------

// Tracer is the package-level tracer instance.
var Tracer = &tracer{}

type ctxSpanKey struct{}

type tracer struct{}

// Start creates a new span derived from ctx and returns the enriched context.
func (t *tracer) Start(ctx context.Context, name string) (context.Context, *Span) {
	traceID := newTraceID()
	if parent, ok := ctx.Value(ctxSpanKey{}).(*Span); ok {
		traceID = parent.TraceID
	}
	s := &Span{
		TraceID: traceID,
		Name:    name,
		Start_:  time.Now(),
		attrs:   make(map[string]string),
	}
	return context.WithValue(ctx, ctxSpanKey{}, s), s
}

// ---------------------------------------------------------------------------
// Span
// ---------------------------------------------------------------------------

// Span represents a single unit of work.
type Span struct {
	TraceID string
	Name    string
	Start_  time.Time
	attrs   map[string]string
}

// SetAttributes attaches key-value metadata to the span.
func (s *Span) SetAttributes(attrs ...SpanAttr) {
	for _, a := range attrs {
		s.attrs[a.Key] = a.Value
	}
}

// End logs the completed span.
func (s *Span) End() {
	dur := time.Since(s.Start_)
	attrs := ""
	for k, v := range s.attrs {
		attrs += fmt.Sprintf(" %s=%q", k, v)
	}
	log.Printf("[trace] id=%s span=%q duration=%s%s", s.TraceID, s.Name, dur, attrs)
}

// ---------------------------------------------------------------------------
// SpanAttr helpers
// ---------------------------------------------------------------------------

// SpanAttr is a key-value attribute attached to a span.
type SpanAttr struct {
	Key, Value string
}

// strAttr creates a string-valued SpanAttr.
func strAttr(key, value string) SpanAttr { return SpanAttr{key, value} }

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

// InitTracing is a no-op in the stdlib implementation; the tracer is
// initialised at package level.  Swap the tracer variable with an OTel SDK
// tracer for production use.
func InitTracing() {}

func newTraceID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck // crypto/rand.Read never fails on any supported platform
	return hex.EncodeToString(b)
}
