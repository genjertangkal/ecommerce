// Package metrics provides the platform's counter, gauge and histogram
// capability.
package metrics

import (
	"sort"
	"sync"
	"time"
)

// Histogram is a bucketed distribution of observations.
//
// The bucket boundaries are fixed at construction time: [lower, upper) pairs,
// plus a final +Inf bucket so that every observation is counted exactly once.
type Histogram struct {
	bounds  []float64
	buckets []int64
	count   int64
	sum     float64
}

// NewHistogram creates a histogram with the given upper bounds. Bounds must be
// sorted ascending; anything at or above the last bound lands in the final
// bucket.
func NewHistogram(bounds []float64) *Histogram {
	sorted := append([]float64(nil), bounds...)
	sort.Float64s(sorted)
	return &Histogram{
		bounds:  sorted,
		buckets: make([]int64, len(sorted)+1),
	}
}

// Observe records a single value.
func (h *Histogram) Observe(v float64) {
	h.count++
	h.sum += v

	// Buckets are [lower, upper), so v belongs to the first bucket whose lower
	// bound is strictly greater than v. A value at or above the last bound lands
	// in the trailing +Inf bucket, which is why there is one more bucket than
	// there are bounds.
	idx := sort.Search(len(h.bounds), func(i int) bool { return h.bounds[i] > v })
	h.buckets[idx]++
}

// Count returns the number of observations.
func (h *Histogram) Count() int64 { return h.count }

// Sum returns the sum of all observations.
func (h *Histogram) Sum() float64 { return h.sum }

// Mean returns the arithmetic mean, or 0 when nothing was observed.
func (h *Histogram) Mean() float64 {
	if h.count == 0 {
		return 0
	}
	return h.sum / float64(h.count)
}

// Buckets returns a copy of the per-bucket counts, ordered by ascending bound.
func (h *Histogram) Buckets() []int64 {
	out := make([]int64, len(h.buckets))
	copy(out, h.buckets)
	return out
}

// Metrics collects counters and gauges for one service.
//
// Metrics is safe for concurrent use.
type Metrics struct {
	serviceName string
	now         func() time.Time

	mu         sync.RWMutex
	counters   map[string]int64
	gauges     map[string]float64
	histograms map[string]*Histogram
}

// NewMetrics creates a Metrics collector for the named service.
func NewMetrics(serviceName string) *Metrics {
	return &Metrics{
		serviceName: serviceName,
		now:         time.Now,
		counters:    make(map[string]int64),
		gauges:      make(map[string]float64),
		histograms:  make(map[string]*Histogram),
	}
}

// SetClock overrides the timestamp source. Intended for tests.
func (m *Metrics) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = now
}

// IncrementCounter adds value to the named counter. Negative values are
// ignored, because a counter that can go down is not a counter.
func (m *Metrics) IncrementCounter(name string, value int64) {
	if value < 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += value
}

// Counter returns the current value of a counter.
func (m *Metrics) Counter(name string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.counters[name]
}

// SetGauge sets a gauge to an absolute value.
func (m *Metrics) SetGauge(name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[name] = value
}

// Gauge returns the current value of a gauge.
func (m *Metrics) Gauge(name string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gauges[name]
}

// Observe records a value into the named histogram, creating it with default
// bounds on first use.
func (m *Metrics) Observe(name string, value float64) {
	m.mu.Lock()
	h, ok := m.histograms[name]
	if !ok {
		h = NewHistogram(defaultHistogramBounds)
		m.histograms[name] = h
	}
	m.mu.Unlock()

	h.Observe(value)
}

// Histogram returns a snapshot of a named histogram, or nil if it does not exist.
func (m *Metrics) Histogram(name string) *Histogram {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.histograms[name]
}

// defaultHistogramBounds covers sub-millisecond to one-minute latencies, which
// is the range most service metrics care about.
var defaultHistogramBounds = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// Flush returns a consistent snapshot of all metrics.
//
// The returned maps are copies, so the caller can serialise them without
// holding the lock (and without racing a concurrent mutation).
func (m *Metrics) Flush() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	counters := make(map[string]int64, len(m.counters))
	for k, v := range m.counters {
		counters[k] = v
	}

	gauges := make(map[string]float64, len(m.gauges))
	for k, v := range m.gauges {
		gauges[k] = v
	}

	histograms := make(map[string]interface{}, len(m.histograms))
	for k, h := range m.histograms {
		histograms[k] = map[string]interface{}{
			"count":   h.Count(),
			"sum":     h.Sum(),
			"mean":    h.Mean(),
			"buckets": h.Buckets(),
		}
	}

	return map[string]interface{}{
		"service":    m.serviceName,
		"timestamp":  m.now().UTC().Format(time.RFC3339),
		"counters":   counters,
		"gauges":     gauges,
		"histograms": histograms,
	}
}
