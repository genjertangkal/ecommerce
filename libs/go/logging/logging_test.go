package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newTestLogger(buf *bytes.Buffer, level Level) *Logger {
	return NewLogger(Config{
		Writer:  buf,
		Level:   level,
		Service: "product-catalog",
		Clock:   func() time.Time { return fixedTime },
	})
}

// decode returns the single entry written to buf.
func decode(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v (raw: %q)", err, buf.String())
	}
	return entry
}

func TestLevelString(t *testing.T) {
	t.Parallel()

	cases := map[Level]string{
		LevelDebug: "DEBUG",
		LevelInfo:  "INFO",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
		Level(99):  "UNKNOWN",
	}

	for level, want := range cases {
		if got := level.String(); got != want {
			t.Errorf("Level(%d).String() = %q, want %q", int(level), got, want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in    string
		want  Level
		valid bool
	}{
		{"DEBUG", LevelDebug, true},
		{"info", LevelInfo, true},
		{"WARNING", LevelWarn, true},
		{"error", LevelError, true},
		{"nope", LevelInfo, false},
	}

	for _, tc := range cases {
		got, ok := ParseLevel(tc.in)
		if ok != tc.valid || (tc.valid && got != tc.want) {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.valid)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelWarn)

	logger.Debug("d")
	logger.Info("i")
	if buf.Len() != 0 {
		t.Errorf("DEBUG and INFO were emitted at threshold WARN: %q", buf.String())
	}

	logger.Warn("w")
	if buf.Len() == 0 {
		t.Error("WARN was not emitted at threshold WARN")
	}
}

func TestEntryShape(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug).WithField("request_id", "abc")

	logger.Info("served", map[string]interface{}{"status": 200})

	entry := decode(t, &buf)
	if got, want := entry["level"], "INFO"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}
	if got, want := entry["service"], "product-catalog"; got != want {
		t.Errorf("service = %v, want %v", got, want)
	}
	if got, want := entry["message"], "served"; got != want {
		t.Errorf("message = %v, want %v", got, want)
	}
	if got, want := entry["timestamp"], fixedTime.Format(time.RFC3339); got != want {
		t.Errorf("timestamp = %v, want %v", got, want)
	}

	fields, ok := entry["fields"].(map[string]interface{})
	if !ok {
		t.Fatalf("fields is %T, want object", entry["fields"])
	}
	if got := fields["request_id"]; got != "abc" {
		t.Errorf("fields.request_id = %v, want %q", got, "abc")
	}
	if got := fields["status"]; got != float64(200) {
		t.Errorf("fields.status = %v (%T), want 200", got, got)
	}
}

func TestWithFieldsDoesNotMutateParent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	parent := newTestLogger(&buf, LevelDebug).WithField("a", 1)
	child := parent.WithField("b", 2)

	parent.Info("from parent")
	parentEntry := decode(t, &buf)
	parentFields := parentEntry["fields"].(map[string]interface{})
	if _, ok := parentFields["b"]; ok {
		t.Error("WithFields leaked the child field back into the parent logger")
	}

	buf.Reset()
	child.Info("from child")
	childEntry := decode(t, &buf)
	childFields := childEntry["fields"].(map[string]interface{})
	if childFields["a"] != float64(1) || childFields["b"] != float64(2) {
		t.Errorf("child fields = %v, want both a and b", childFields)
	}
}

func TestCallSiteFieldsOverrideLoggerFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug).WithField("k", "logger")

	logger.Info("m", map[string]interface{}{"k": "call"})

	fields := decode(t, &buf)["fields"].(map[string]interface{})
	if got := fields["k"]; got != "call" {
		t.Errorf("fields.k = %v, want %q (the call site is the more specific context)", got, "call")
	}
}

func TestWithContextPicksUpContextFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug)

	ctx := ContextWithFields(context.Background(), map[string]interface{}{"trace_id": "t-1"})
	logger.WithContext(ctx).Info("m")

	fields := decode(t, &buf)["fields"].(map[string]interface{})
	if got := fields["trace_id"]; got != "t-1" {
		t.Errorf("fields.trace_id = %v, want %q", got, "t-1")
	}
}

func TestWithContextOnPlainContextIsNoOp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug).WithField("keep", "me")

	logger.WithContext(context.Background()).Info("m")

	fields := decode(t, &buf)["fields"].(map[string]interface{})
	if got := fields["keep"]; got != "me" {
		t.Errorf("fields.keep = %v, want %q", got, "me")
	}

	//nolint:staticcheck // deliberately exercising the nil-context guard
	if got := logger.WithContext(nil); got != logger {
		t.Error("WithContext(nil) should return the same logger")
	}
}

func TestDefaultConfigAndNilWriter(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg.Writer == nil {
		t.Error("DefaultConfig().Writer is nil")
	}
	if cfg.Level != LevelInfo {
		t.Errorf("DefaultConfig().Level = %v, want LevelInfo", cfg.Level)
	}

	// A zero Config must not panic; it falls back to stdout.
	if l := NewLogger(Config{}); l == nil {
		t.Error("NewLogger(Config{}) returned nil")
	}
}

func TestSyncOnNonSyncingWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := newTestLogger(&buf, LevelDebug).Sync(); err != nil {
		t.Errorf("Sync() on a bytes.Buffer = %v, want nil", err)
	}
}

// failingWriter exercises the "nowhere to report an encoding failure" path.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("disk full") }

func TestLogToFailingWriterDoesNotPanic(t *testing.T) {
	t.Parallel()

	logger := NewLogger(Config{Writer: failingWriter{}, Level: LevelDebug})
	logger.Info("m")
}
