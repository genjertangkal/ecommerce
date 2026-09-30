package http

import (
	"context"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig(baseURL string) ClientConfig {
	cfg := DefaultClientConfig()
	cfg.BaseURL = baseURL
	// Keep the retry tests fast: no real backoff.
	cfg.Backoff = func(int) time.Duration { return 0 }
	return cfg
}

func TestGetReturnsBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		_, _ = io.WriteString(w, `{"hello":"world"}`)
	}))
	defer srv.Close()

	resp, err := NewClient(testConfig(srv.URL)).DoRequest(nethttp.MethodGet, "/x", nil)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}

	if !resp.OK() {
		t.Errorf("OK() = false for status %d", resp.StatusCode)
	}

	var got map[string]string
	if err := resp.JSON(&got); err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("hello = %q, want %q", got["hello"], "world")
	}
}

func TestDefaultHeadersAreApplied(t *testing.T) {
	t.Parallel()

	var gotUserAgent, gotXCustom string
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		gotXCustom = r.Header.Get("X-Custom")
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Headers["User-Agent"] = "ecommerce-test"
	cfg.Headers["X-Custom"] = "custom-value"

	if _, err := NewClient(cfg).DoRequest(nethttp.MethodGet, "/x", nil); err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}

	if gotUserAgent != "ecommerce-test" {
		t.Errorf("User-Agent = %q, want %q", gotUserAgent, "ecommerce-test")
	}
	if gotXCustom != "custom-value" {
		t.Errorf("X-Custom = %q, want %q", gotXCustom, "custom-value")
	}
}

// TestRetriesReplayTheBody is the regression test for the bug where every retry
// reused an already-drained request body.
func TestRetriesReplayTheBody(t *testing.T) {
	t.Parallel()

	var attempts int32
	bodies := make([]string, 0, 3)

	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		atomic.AddInt32(&attempts, 1)
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.WriteHeader(nethttp.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.MaxRetries = 2

	_, err := NewClient(cfg).DoRequest(nethttp.MethodPost, "/x", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("server saw %d attempts, want 3 (initial + 2 retries)", got)
	}
	for i, body := range bodies {
		if body != `{"k":"v"}` {
			t.Errorf("attempt %d body = %q, want %q", i+1, body, `{"k":"v"}`)
		}
	}
}

func TestDoesNotRetryOn4xx(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(nethttp.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.MaxRetries = 3

	resp, err := NewClient(cfg).DoRequest(nethttp.MethodGet, "/x", nil)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}

	if resp.StatusCode != nethttp.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, nethttp.StatusNotFound)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("server saw %d attempts, want 1: a 404 is not retryable", got)
	}
}

func TestLastRetryResponseIsReturned(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.MaxRetries = 1

	resp, err := NewClient(cfg).DoRequest(nethttp.MethodGet, "/x", nil)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}

	if resp.StatusCode != nethttp.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d so the caller can inspect it",
			resp.StatusCode, nethttp.StatusServiceUnavailable)
	}
}

func TestZeroRetriesMeansSingleAttempt(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(nethttp.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.MaxRetries = 0

	resp, err := NewClient(cfg).DoRequest(nethttp.MethodGet, "/x", nil)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	if resp.StatusCode != nethttp.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, nethttp.StatusInternalServerError)
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("server saw %d attempts, want 1", got)
	}
}

func TestContextCancellationIsNotRetried(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		atomic.AddInt32(&attempts, 1)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := testConfig(srv.URL)
	cfg.MaxRetries = 3

	if _, err := NewClient(cfg).DoRequest(nethttp.MethodGet, "/x", nil, WithContext(ctx)); err == nil {
		t.Fatal("DoRequest() with a cancelled context returned nil error")
	}
	if got := atomic.LoadInt32(&attempts); got != 0 {
		t.Errorf("server saw %d attempts, want 0 for a cancelled context", got)
	}
}

func TestWithHeaderOptionOverridesDefault(t *testing.T) {
	t.Parallel()

	var gotAccept string
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		gotAccept = r.Header.Get("Accept")
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.Headers["Accept"] = "application/xml"

	if _, err := NewClient(cfg).DoRequest(nethttp.MethodGet, "/x", nil,
		WithHeader("Accept", "application/json")); err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want %q (per-request options win)", gotAccept, "application/json")
	}
}

func TestWithJSONBodyOption(t *testing.T) {
	t.Parallel()

	var gotBody, gotContentType string
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotContentType = r.Header.Get("Content-Type")
	}))
	defer srv.Close()

	if _, err := NewClient(testConfig(srv.URL)).DoRequest(nethttp.MethodPost, "/x", nil,
		WithJSONBody(map[string]string{"k": "v"})); err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}

	if gotBody != `{"k":"v"}` {
		t.Errorf("body = %q, want %q", gotBody, `{"k":"v"}`)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
}

// TestWithJSONBodyOptionIsReplayedOnRetry proves the body installed by an option
// is also replayable, not just the one built from the `body` argument.
func TestWithJSONBodyOptionIsReplayedOnRetry(t *testing.T) {
	t.Parallel()

	var attempts int32
	bodies := make([]string, 0, 2)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		atomic.AddInt32(&attempts, 1)
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.WriteHeader(nethttp.StatusBadGateway)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL)
	cfg.MaxRetries = 1

	if _, err := NewClient(cfg).DoRequest(nethttp.MethodPost, "/x", nil,
		WithJSONBody(map[string]string{"k": "v"})); err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}

	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("server saw %d attempts, want 2", got)
	}
	for i, body := range bodies {
		if body != `{"k":"v"}` {
			t.Errorf("attempt %d body = %q, want %q", i+1, body, `{"k":"v"}`)
		}
	}
}

func TestWithJSONBodyReportsMarshalError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		t.Error("server must not be reached when the body cannot be encoded")
	}))
	defer srv.Close()

	// A channel cannot be JSON-encoded.
	_, err := NewClient(testConfig(srv.URL)).DoRequest(nethttp.MethodPost, "/x", nil,
		WithJSONBody(make(chan int)))
	if err == nil {
		t.Fatal("DoRequest() = nil error, want a marshal error")
	}
	if !strings.Contains(err.Error(), "encoding JSON body") {
		t.Errorf("error = %v, want it to mention JSON encoding", err)
	}
}

func TestUnencodableBodyArgumentIsReported(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		t.Error("server must not be reached")
	}))
	defer srv.Close()

	if _, err := NewClient(testConfig(srv.URL)).Post("/x", make(chan int)); err == nil {
		t.Fatal("Post() = nil error, want a marshal error")
	}
}

func TestShouldRetryStatus(t *testing.T) {
	t.Parallel()

	retryable := []int{429, 500, 502, 503, 504}
	for _, code := range retryable {
		if !shouldRetryStatus(code) {
			t.Errorf("shouldRetryStatus(%d) = false, want true", code)
		}
	}

	notRetryable := []int{200, 201, 301, 400, 401, 403, 404, 409, 501}
	for _, code := range notRetryable {
		if shouldRetryStatus(code) {
			t.Errorf("shouldRetryStatus(%d) = true, want false", code)
		}
	}
}

func TestDefaultConfigValues(t *testing.T) {
	t.Parallel()

	cfg := DefaultClientConfig()
	if cfg.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, DefaultTimeout)
	}
	if cfg.MaxRetries != DefaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, DefaultMaxRetries)
	}
	if cfg.Headers == nil {
		t.Error("Headers is nil; a nil map would panic when NewClient fills defaults")
	}
}

func TestNewClientDefaultsAreAppliedToZeroConfig(t *testing.T) {
	t.Parallel()

	c := NewClient(ClientConfig{})
	if c.config.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.config.Timeout, DefaultTimeout)
	}
	if c.config.Backoff == nil {
		t.Error("Backoff is nil")
	}
	if c.config.Headers == nil {
		t.Error("Headers is nil")
	}
}

func TestZeroConfigDoesNotMutateCallerHeaders(t *testing.T) {
	t.Parallel()

	cfg := ClientConfig{}
	c := NewClient(cfg)
	c.config.Headers["X"] = "1"

	if cfg.Headers != nil {
		t.Error("NewClient wrote into the caller's zero-valued Config")
	}
}

func TestResponseJSONOnInvalidBody(t *testing.T) {
	t.Parallel()

	r := &Response{Body: []byte("not json")}
	if err := r.JSON(&map[string]interface{}{}); err == nil {
		t.Error("JSON() = nil error for invalid JSON, want an error")
	}
}

func TestResponseOKRange(t *testing.T) {
	t.Parallel()

	cases := map[int]bool{200: true, 204: true, 299: true, 300: false, 404: false, 500: false}
	for code, want := range cases {
		if got := (&Response{StatusCode: code}).OK(); got != want {
			t.Errorf("OK() for %d = %v, want %v", code, got, want)
		}
	}
}
