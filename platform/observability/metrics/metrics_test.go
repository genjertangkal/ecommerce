package metrics

import (
	"sync"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestIncrementCounter(t *testing.T) {
	t.Parallel()

	m := NewMetrics("product-catalog")
	m.IncrementCounter("requests", 2)
	m.IncrementCounter("requests", 3)

	if got := m.Counter("requests"); got != 5 {
		t.Errorf("Counter(requests) = %d, want 5", got)
	}
	if got := m.Counter("missing"); got != 0 {
		t.Errorf("Counter(missing) = %d, want 0", got)
	}
}

func TestCounterIgnoresNegativeValues(t *testing.T) {
	t.Parallel()

	m := NewMetrics("svc")
	m.IncrementCounter("c", 5)
	m.IncrementCounter("c", -3)

	if got := m.Counter("c"); got != 5 {
		t.Errorf("Counter(c) = %d, want 5: a counter must not decrease", got)
	}
}

func TestGauges(t *testing.T) {
	t.Parallel()

	m := NewMetrics("svc")
	m.SetGauge("queue_depth", 12.5)
	m.SetGauge("queue_depth", 3.5)

	if got := m.Gauge("queue_depth"); got != 3.5 {
		t.Errorf("Gauge(queue_depth) = %v, want 3.5 (a gauge is absolute, not additive)", got)
	}
	if got := m.Gauge("missing"); got != 0 {
		t.Errorf("Gauge(missing) = %v, want 0", got)
	}
}

func TestHistogramBucketing(t *testing.T) {
	t.Parallel()

	h := NewHistogram([]float64{1, 5, 10})

	// 0.5 -> first bucket, 1 -> second, 3 -> second, 5 -> third,
	// 10 and 100 -> final (+Inf) bucket.
	for _, v := range []float64{0.5, 1, 3, 5, 10, 100} {
		h.Observe(v)
	}

	if got := h.Count(); got != 6 {
		t.Errorf("Count() = %d, want 6", got)
	}
	wantSum := 0.5 + 1 + 3 + 5 + 10 + 100
	if got := h.Sum(); got != wantSum {
		t.Errorf("Sum() = %v, want %v", got, wantSum)
	}

	buckets := h.Buckets()
	if len(buckets) != 4 {
		t.Fatalf("Buckets() has %d entries, want 4 (3 bounds + +Inf)", len(buckets))
	}
	want := []int64{1, 2, 1, 2}
	for i := range want {
		if buckets[i] != want[i] {
			t.Errorf("bucket %d = %d, want %d (all buckets %v)", i, buckets[i], want[i], buckets)
		}
	}
}

func TestHistogramEveryObservationIsCounted(t *testing.T) {
	t.Parallel()

	h := NewHistogram([]float64{1})
	var total int64
	for _, v := range []float64{-100, 0, 0.999, 1, 1000} {
		h.Observe(v)
		total++
	}

	if h.Count() != total {
		t.Errorf("Count() = %d, want %d", h.Count(), total)
	}
	var sum int64
	for _, b := range h.Buckets() {
		sum += b
	}
	if sum != total {
		t.Errorf("bucket counts sum to %d, want %d", sum, total)
	}
}

func TestHistogramEmptyMean(t *testing.T) {
	t.Parallel()

	h := NewHistogram([]float64{1})
	if got := h.Mean(); got != 0 {
		t.Errorf("Mean() on an empty histogram = %v, want 0", got)
	}
}

func TestHistogramBoundsAreSorted(t *testing.T) {
	t.Parallel()

	h := NewHistogram([]float64{10, 1, 5})
	h.Observe(7)

	buckets := h.Buckets()
	// Sorted bounds are [1 5 10], giving buckets
	//   0: (-inf, 1)   1: [1, 5)   2: [5, 10)   3: [10, +inf)
	// A value of 7 belongs in [5, 10), which is bucket 2.
	if buckets[2] != 1 {
		t.Errorf("bucket counts = %v, want the observation in bucket 2", buckets)
	}
}

func TestBucketsReturnsACopy(t *testing.T) {
	t.Parallel()

	h := NewHistogram([]float64{1})
	h.Observe(0.5)

	buckets := h.Buckets()
	buckets[0] = 999

	if got := h.Buckets()[0]; got != 1 {
		t.Errorf("Buckets() leaked the internal slice: got %d, want 1", got)
	}
}

func TestObserveCreatesDefaultHistogram(t *testing.T) {
	t.Parallel()

	m := NewMetrics("svc")
	m.Observe("latency", 0.02)

	h := m.Histogram("latency")
	if h == nil {
		t.Fatal("Histogram(latency) = nil, want a lazily created histogram")
	}
	if h.Count() != 1 {
		t.Errorf("Count() = %d, want 1", h.Count())
	}
}

func TestFlushSnapshot(t *testing.T) {
	t.Parallel()

	m := NewMetrics("product-catalog")
	m.SetClock(func() time.Time { return fixedTime })
	m.IncrementCounter("requests", 7)
	m.SetGauge("queue_depth", 2)
	m.Observe("latency", 0.01)

	snapshot := m.Flush()
	if got, want := snapshot["service"], "product-catalog"; got != want {
		t.Errorf("service = %v, want %v", got, want)
	}
	if got, want := snapshot["timestamp"], fixedTime.Format(time.RFC3339); got != want {
		t.Errorf("timestamp = %v, want %v", got, want)
	}

	counters, ok := snapshot["counters"].(map[string]int64)
	if !ok {
		t.Fatalf("counters = %T, want map[string]int64", snapshot["counters"])
	}
	if counters["requests"] != 7 {
		t.Errorf("counters[requests] = %d, want 7", counters["requests"])
	}

	gauges, ok := snapshot["gauges"].(map[string]float64)
	if !ok {
		t.Fatalf("gauges = %T, want map[string]float64", snapshot["gauges"])
	}
	if gauges["queue_depth"] != 2 {
		t.Errorf("gauges[queue_depth] = %v, want 2", gauges["queue_depth"])
	}

	histograms, ok := snapshot["histograms"].(map[string]interface{})
	if !ok {
		t.Fatalf("histograms = %T, want a map", snapshot["histograms"])
	}
	if _, ok := histograms["latency"]; !ok {
		t.Error("histograms is missing the latency entry")
	}
}

// A Flush() caller serialises the snapshot after the lock is released, so the
// maps must be copies or the race detector will (correctly) complain.
func TestFlushDoesNotAliasInternalState(t *testing.T) {
	t.Parallel()

	m := NewMetrics("svc")
	m.IncrementCounter("requests", 1)

	snapshot := m.Flush()
	counters := snapshot["counters"].(map[string]int64)
	counters["requests"] = 999

	if got := m.Counter("requests"); got != 1 {
		t.Errorf("Counter(requests) = %d, want 1: Flush() leaked the internal map", got)
	}
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	m := NewMetrics("svc")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.IncrementCounter("c", 1)
			m.SetGauge("g", float64(i))
			m.Observe("h", float64(i))
			m.Flush()
		}(i)
	}
	wg.Wait()

	if got := m.Counter("c"); got != 50 {
		t.Errorf("Counter(c) = %d, want 50", got)
	}
}
