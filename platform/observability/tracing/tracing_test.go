package tracing

import (
	"context"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newTestTracer(rate float64) (*Tracer, *time.Time) {
	tr := NewTracer("product-catalog", rate)
	now := fixedTime
	tr.SetClock(func() time.Time {
		now = now.Add(time.Millisecond)
		return now
	})
	return tr, &now
}

func TestSamplerRateIsClamped(t *testing.T) {
	t.Parallel()

	if got := NewTracer("svc", -1).SamplerRate(); got != 0 {
		t.Errorf("SamplerRate() for -1 = %v, want 0", got)
	}
	if got := NewTracer("svc", 5).SamplerRate(); got != 1 {
		t.Errorf("SamplerRate() for 5 = %v, want 1", got)
	}
	if got := NewTracer("svc", 0.25).SamplerRate(); got != 0.25 {
		t.Errorf("SamplerRate() = %v, want 0.25", got)
	}
}

func TestStartSpanReturnsSpanInContext(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	ctx, span := tr.StartSpan(context.Background(), "ListProducts")

	if span == nil {
		t.Fatal("StartSpan returned a nil span")
	}
	if span.Operation != "ListProducts" {
		t.Errorf("Operation = %q, want %q", span.Operation, "ListProducts")
	}
	if span.Service != "product-catalog" {
		t.Errorf("Service = %q, want %q", span.Service, "product-catalog")
	}
	if got := SpanFromContext(ctx); got != span {
		t.Errorf("SpanFromContext() = %v, want the span returned by StartSpan", got)
	}
}

func TestChildSpanInheritsTraceID(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	ctx, parent := tr.StartSpan(context.Background(), "parent")
	_, child := tr.StartSpan(ctx, "child")

	if child.TraceID != parent.TraceID {
		t.Errorf("child TraceID = %q, want the parent's %q", child.TraceID, parent.TraceID)
	}
	if child.ParentSpanID != parent.SpanID {
		t.Errorf("child ParentSpanID = %q, want the parent's SpanID %q", child.ParentSpanID, parent.SpanID)
	}
	if child.SpanID == parent.SpanID {
		t.Error("child and parent share a SpanID")
	}
}

func TestRootSpanHasNoParent(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	_, span := tr.StartSpan(context.Background(), "root")

	if span.ParentSpanID != "" {
		t.Errorf("ParentSpanID = %q, want empty for a root span", span.ParentSpanID)
	}
}

func TestNilContextIsAccepted(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	ctx, span := tr.StartSpan(nil, "op") //nolint:staticcheck // exercising the nil guard

	if ctx == nil {
		t.Fatal("StartSpan(nil) returned a nil context")
	}
	if SpanFromContext(ctx) != span {
		t.Error("the span is not reachable from the returned context")
	}
}

// The previous implementation created spans with a nil `tags` map and then did
// `s.tags[key] = value`, which panics with "assignment to entry in nil map".
func TestTagDoesNotPanic(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	_, span := tr.StartSpan(context.Background(), "op")

	span.Tag("product_id", "p-1")
	span.Tag("attempt", 2)

	tags := span.Tags()
	if tags["product_id"] != "p-1" {
		t.Errorf("tags[product_id] = %v, want %q", tags["product_id"], "p-1")
	}
	if tags["attempt"] != 2 {
		t.Errorf("tags[attempt] = %v, want 2", tags["attempt"])
	}
}

func TestTagOnNilSpanIsSafe(t *testing.T) {
	t.Parallel()

	var span *Span
	span.Tag("k", "v") //nolint:staticcheck // exercising the nil guard
	span.End()         //nolint:staticcheck // exercising the nil guard

	if got := span.Tags(); got != nil {
		t.Errorf("Tags() on a nil span = %v, want nil", got)
	}
	if got := span.Duration(); got != 0 {
		t.Errorf("Duration() on a nil span = %v, want 0", got)
	}
}

func TestTagAfterEndIsIgnored(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	_, span := tr.StartSpan(context.Background(), "op")
	span.Tag("before", 1)
	span.End()
	span.Tag("after", 2)

	if _, present := span.Tags()["after"]; present {
		t.Error("a tag was recorded on an ended span")
	}
}

func TestEndIsIdempotent(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	_, span := tr.StartSpan(context.Background(), "op")

	span.End()
	first := span.Duration()
	span.End()
	span.End()

	if got := span.Duration(); got != first {
		t.Errorf("Duration() changed after repeated End(): %v then %v", first, got)
	}
	if first == 0 {
		t.Error("Duration() = 0 for an ended span")
	}
}

func TestDurationIsZeroUntilEnd(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	_, span := tr.StartSpan(context.Background(), "op")

	if got := span.Duration(); got != 0 {
		t.Errorf("Duration() before End() = %v, want 0", got)
	}
}

func TestEndSpanThroughTracer(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	_, span := tr.StartSpan(context.Background(), "op")

	tr.EndSpan(span)
	if span.Duration() == 0 {
		t.Error("EndSpan did not end the span")
	}

	tr.EndSpan(nil) // must not panic
}

// With samplerRate 0 the span must still be usable, just not retained.
func TestZeroSampleRateStillProducesUsableSpans(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(0)
	ctx, span := tr.StartSpan(context.Background(), "op")

	if span.Sampled() {
		t.Error("Sampled() = true at sampler rate 0")
	}
	span.Tag("k", "v")
	span.End()

	if SpanFromContext(ctx) != span {
		t.Error("span is not reachable from the context at sampler rate 0")
	}
}

func TestTagsReturnsACopy(t *testing.T) {
	t.Parallel()

	tr, _ := newTestTracer(1)
	_, span := tr.StartSpan(context.Background(), "op")
	span.Tag("k", "v")

	tags := span.Tags()
	tags["k"] = "mutated"
	delete(tags, "k")

	if got := span.Tags()["k"]; got != "v" {
		t.Errorf("Tags() leaked internal state: got %v, want %q", got, "v")
	}
}

func TestSpanFromContextOnEmptyContext(t *testing.T) {
	t.Parallel()

	if got := SpanFromContext(context.Background()); got != nil {
		t.Errorf("SpanFromContext(background) = %v, want nil", got)
	}
	//nolint:staticcheck // exercising the nil guard
	if got := SpanFromContext(nil); got != nil {
		t.Errorf("SpanFromContext(nil) = %v, want nil", got)
	}
}

func TestAccessors(t *testing.T) {
	t.Parallel()

	tr := NewTracer("product-catalog", 0.5)
	if got := tr.Service(); got != "product-catalog" {
		t.Errorf("Service() = %q, want %q", got, "product-catalog")
	}
}

func TestSetClockIgnoresNil(t *testing.T) {
	t.Parallel()

	tr := NewTracer("svc", 1)
	tr.SetClock(nil)
	_, span := tr.StartSpan(context.Background(), "op")
	span.End()

	if span.Duration() <= 0 {
		t.Error("the default clock was replaced by a nil clock")
	}
}
