// Package tracing provides the platform's distributed tracing capability.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Tracer creates spans and records them for the lifetime of the process.
//
// The in-memory span store is intentionally simple: this capability exists to
// give services a uniform span API, not to be a full OpenTelemetry
// implementation.
type Tracer struct {
	serviceName string
	samplerRate float64
	now         func() time.Time

	mu    sync.RWMutex
	spans map[string]*Span
}

// NewTracer creates a Tracer for the named service. samplerRate is clamped to
// [0, 1]; a rate of 0 disables sampling, 1 samples everything.
func NewTracer(serviceName string, samplerRate float64) *Tracer {
	switch {
	case samplerRate < 0:
		samplerRate = 0
	case samplerRate > 1:
		samplerRate = 1
	}
	return &Tracer{
		serviceName: serviceName,
		samplerRate: samplerRate,
		now:         time.Now,
		spans:       make(map[string]*Span),
	}
}

// Service reports the service name attached to every span.
func (t *Tracer) Service() string { return t.serviceName }

// SamplerRate reports the configured sampling rate.
func (t *Tracer) SamplerRate() float64 { return t.samplerRate }

// SetClock overrides the timestamp source. Intended for tests.
func (t *Tracer) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.now = now
}

// contextKey is unexported so that no other package can collide with it.
type contextKey struct{}

// spanContextKey carries the active span through a context.Context.
var spanContextKey contextKey

// Span is a single traced operation.
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Operation    string
	Service      string

	tracer    *Tracer
	startTime time.Time
	endTime   time.Time
	sampled   bool

	mu      sync.RWMutex
	tags    map[string]interface{}
	ended   bool
	endOnce sync.Once
}

// StartSpan begins a span and returns a context carrying it.
func (t *Tracer) StartSpan(ctx context.Context, operation string) (context.Context, *Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	sampled := t.samplerRate > 0

	span := &Span{
		TraceID:   newID(t),
		SpanID:    newID(t),
		Operation: operation,
		Service:   t.serviceName,
		tracer:    t,
		startTime: t.now(),
		sampled:   sampled,
		tags:      make(map[string]interface{}),
	}

	if parent := SpanFromContext(ctx); parent != nil {
		span.TraceID = parent.TraceID
		span.ParentSpanID = parent.SpanID
	}

	if sampled {
		t.mu.Lock()
		t.spans[span.SpanID] = span
		t.mu.Unlock()
	}

	return ContextWithSpan(ctx, span), span
}

// EndSpan ends a span and removes it from the tracer's store. Calling it more
// than once is safe and only records the first end time.
func (t *Tracer) EndSpan(span *Span) {
	if span == nil {
		return
	}
	span.End()
}

// SpanFromContext returns the active span, or nil.
func SpanFromContext(ctx context.Context) *Span {
	if ctx == nil {
		return nil
	}
	span, _ := ctx.Value(spanContextKey).(*Span)
	return span
}

// ContextWithSpan returns a context carrying span as the active span.
func ContextWithSpan(ctx context.Context, span *Span) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, spanContextKey, span)
}

// Tag adds a tag to the span. Tagging an ended span is a no-op.
func (s *Span) Tag(key string, value interface{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.tags[key] = value
}

// Tags returns a copy of the span's tags.
func (s *Span) Tags() map[string]interface{} {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]interface{}, len(s.tags))
	for k, v := range s.tags {
		out[k] = v
	}
	return out
}

// Duration returns how long the span ran. It is 0 until the span ends.
func (s *Span) Duration() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.endTime.IsZero() {
		return 0
	}
	return s.endTime.Sub(s.startTime)
}

// Sampled reports whether this span is retained.
func (s *Span) Sampled() bool { return s != nil && s.sampled }

// End finishes the span. It is idempotent.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.mu.Lock()
		s.endTime = s.tracer.now()
		s.ended = true
		s.mu.Unlock()

		if s.sampled {
			s.tracer.mu.Lock()
			delete(s.tracer.spans, s.SpanID)
			s.tracer.mu.Unlock()
		}
	})
}

// newID returns a random hex identifier, falling back to a counter-derived
// value if the system entropy source fails.
func newID(t *Tracer) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		return hex.EncodeToString([]byte{byte(len(t.spans))})
	}
	return hex.EncodeToString(buf[:])
}
