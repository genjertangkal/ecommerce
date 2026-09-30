// Package http provides a small, dependency-free HTTP client with retries.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"time"
)

// DefaultTimeout is used when a ClientConfig leaves Timeout unset.
const DefaultTimeout = 30 * time.Second

// DefaultMaxRetries is used when a ClientConfig leaves MaxRetries unset.
const DefaultMaxRetries = 3

// ClientConfig configures a Client.
type ClientConfig struct {
	// Timeout bounds a single request attempt.
	Timeout time.Duration
	// MaxRetries is the number of *additional* attempts after the first one
	// fails with a retryable error. Zero means "no retries".
	MaxRetries int
	// BaseURL is prefixed to every request path.
	BaseURL string
	// Headers are applied to every request before the per-request options run.
	Headers map[string]string
	// Backoff is the delay before retry number n (1-based). When nil, a
	// linear backoff is used.
	Backoff func(attempt int) time.Duration
	// HTTPClient overrides the underlying client. When set, Timeout is ignored,
	// because http.Client carries its own timeout.
	HTTPClient *nethttp.Client
}

// DefaultClientConfig returns a config with production-safe defaults.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		Timeout:    DefaultTimeout,
		MaxRetries: DefaultMaxRetries,
		Headers:    make(map[string]string),
	}
}

func (c ClientConfig) withDefaults() ClientConfig {
	if c.HTTPClient == nil && c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Headers == nil {
		c.Headers = make(map[string]string)
	}
	if c.Backoff == nil {
		c.Backoff = defaultBackoff
	}
	return c
}

// defaultBackoff is a linear backoff: 100ms, 200ms, 300ms, ...
func defaultBackoff(attempt int) time.Duration {
	return time.Duration(attempt) * 100 * time.Millisecond
}

// Client is an HTTP client with retries and default headers.
type Client struct {
	client *nethttp.Client
	config ClientConfig
}

// NewClient creates a Client from config.
func NewClient(config ClientConfig) *Client {
	config = config.withDefaults()

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &nethttp.Client{Timeout: config.Timeout}
	}

	return &Client{
		client: httpClient,
		config: config,
	}
}

// RequestOption customises a single request.
//
// The signature returns an error because an option can fail: WithJSONBody has
// to marshal a value, and silently dropping the body on a marshal failure
// would send a truncated request that looks successful to the server.
type RequestOption func(*nethttp.Request) error

// WithHeader sets a header on the request.
func WithHeader(key, value string) RequestOption {
	return func(req *nethttp.Request) error {
		req.Header.Set(key, value)
		return nil
	}
}

// WithContext attaches a context to the request.
func WithContext(ctx context.Context) RequestOption {
	return func(req *nethttp.Request) error {
		*req = *req.WithContext(ctx)
		return nil
	}
}

// WithJSONBody encodes v as the request body and sets Content-Type.
func WithJSONBody(v interface{}) RequestOption {
	return func(req *nethttp.Request) error {
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("http: encoding JSON body: %w", err)
		}
		setRequestBody(req, data)
		req.Header.Set("Content-Type", "application/json")
		return nil
	}
}

// setRequestBody installs a replayable body. GetBody is what lets
// http.Transport rewind the body when it retries or redirects.
func setRequestBody(req *nethttp.Request, data []byte) {
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

// Get performs a GET request.
func (c *Client) Get(path string, opts ...RequestOption) (*nethttp.Response, error) {
	return c.doRequest(nethttp.MethodGet, path, nil, opts...)
}

// Post performs a POST request.
func (c *Client) Post(path string, body interface{}, opts ...RequestOption) (*nethttp.Response, error) {
	return c.doRequest(nethttp.MethodPost, path, body, opts...)
}

// Put performs a PUT request.
func (c *Client) Put(path string, body interface{}, opts ...RequestOption) (*nethttp.Response, error) {
	return c.doRequest(nethttp.MethodPut, path, body, opts...)
}

// Delete performs a DELETE request.
func (c *Client) Delete(path string, opts ...RequestOption) (*nethttp.Response, error) {
	return c.doRequest(nethttp.MethodDelete, path, nil, opts...)
}

// doRequest performs a request, retrying retryable failures.
//
// The request is rebuilt from `body` on every attempt. Reusing a single
// *http.Request would send an already-drained body on each retry, which is the
// classic "works in tests, truncates in production" bug.
func (c *Client) doRequest(method, path string, body interface{}, opts ...RequestOption) (*nethttp.Response, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("http: encoding %s %s: %w", method, path, err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := c.config.Backoff(attempt)
			if delay > 0 {
				time.Sleep(delay)
			}
		}

		req, err := c.buildRequest(method, path, encoded, opts...)
		if err != nil {
			return nil, err
		}

		resp, err := c.client.Do(req)
		if err == nil && !shouldRetryStatus(resp.StatusCode) {
			return resp, nil
		}

		if err != nil {
			lastErr = err
			if resp != nil {
				// A non-nil response means the body must be drained and closed
				// before the connection can be reused.
				drainAndClose(resp)
			}
			if !shouldRetryError(err) {
				return nil, err
			}
			continue
		}

		// 5xx or 429: retryable status, but the body is handed back to the
		// caller on the final attempt so it can be inspected.
		if attempt == c.config.MaxRetries {
			return resp, nil
		}
		lastErr = fmt.Errorf("http: %s %s returned %d", method, path, resp.StatusCode)
		drainAndClose(resp)
	}

	if lastErr == nil {
		lastErr = errors.New("http: request failed with no error recorded")
	}
	return nil, lastErr
}

func (c *Client) buildRequest(method, path string, encoded []byte, opts ...RequestOption) (*nethttp.Request, error) {
	url := c.config.BaseURL + path

	var reqBody io.Reader
	if encoded != nil {
		reqBody = bytes.NewReader(encoded)
	}

	req, err := nethttp.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	if encoded != nil {
		setRequestBody(req, encoded)
	}

	for k, v := range c.config.Headers {
		req.Header.Set(k, v)
	}
	for _, opt := range opts {
		if err := opt(req); err != nil {
			return nil, err
		}
	}

	return req, nil
}

// shouldRetryStatus reports whether a status code is worth another attempt.
func shouldRetryStatus(code int) bool {
	switch {
	case code == nethttp.StatusTooManyRequests:
		return true
	case code >= 500 && code != nethttp.StatusNotImplemented:
		return true
	default:
		return false
	}
}

// shouldRetryError reports whether a transport error is worth another attempt.
// Context cancellation and deadline expiry are the caller's decision, not ours.
func shouldRetryError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// drainAndClose releases a response so the connection can be reused.
func drainAndClose(resp *nethttp.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}

// Response is a fully-read HTTP response.
type Response struct {
	StatusCode int
	Body       []byte
	Headers    nethttp.Header
}

// DoRequest performs a request and reads the whole response body.
//
// The body is always drained and closed before returning, so the connection is
// returned to the pool rather than leaked.
func (c *Client) DoRequest(method, path string, body interface{}, opts ...RequestOption) (*Response, error) {
	resp, err := c.doRequest(method, path, body, opts...)
	if err != nil {
		return nil, err
	}
	defer drainAndClose(resp)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       data,
		Headers:    resp.Header,
	}, nil
}

// JSON decodes the response body into v.
func (r *Response) JSON(v interface{}) error {
	return json.Unmarshal(r.Body, v)
}

// OK reports whether the status code is in the 2xx range.
func (r *Response) OK() bool {
	return r.StatusCode >= 200 && r.StatusCode < 300
}
