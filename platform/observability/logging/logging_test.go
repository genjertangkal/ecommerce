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
	return NewLoggerWithConfig("product-catalog", Config{
		Writer: buf,
		Level:  level,
		Clock:  func() time.Time { return fixedTime },
	})
}

func decode(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	if buf.Len() == 0 {
		t.Fatal("no log entry was written")
	}
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
		Level(42):  "UNKNOWN",
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
		{"warning", LevelWarn, true},
		{"ERROR", LevelError, true},
		{"bogus", LevelInfo, false},
	}
	for _, tc := range cases {
		got, ok := ParseLevel(tc.in)
		if ok != tc.valid || (tc.valid && got != tc.want) {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.valid)
		}
	}
}

// The previous implementation did
//
//	json.NewEncoder(ctx.Value("writer").(interface{ Write([]byte) (int, error) })).Encode(entry)
//
// which panicked on every log call, because a background context has no
// "writer" value and the type assertion on nil has no comma-ok form there.
func TestLoggingNeverTouchesTheContextForItsWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug)

	// A context with nothing in it is the case that used to panic.
	logger.Info(context.Background(), "hello", nil)

	entry := decode(t, &buf)
	if got, want := entry["message"], "hello"; got != want {
		t.Errorf("message = %v, want %v", got, want)
	}
}

func TestNilContextIsAccepted(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug)

	logger.Info(nil, "m", nil) //nolint:staticcheck // exercising the nil guard

	if buf.Len() == 0 {
		t.Error("no entry written for a nil context")
	}
}

func TestLevelFiltering(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelWarn)

	logger.Debug(context.Background(), "d", nil)
	logger.Info(context.Background(), "i", nil)
	if buf.Len() != 0 {
		t.Errorf("entries below the threshold were emitted: %q", buf.String())
	}

	logger.Warn(context.Background(), "w", nil)
	if buf.Len() == 0 {
		t.Error("WARN was not emitted at threshold WARN")
	}
}

func TestEntryFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug)

	logger.Error(context.Background(), "boom", map[string]interface{}{"err": "nope"})

	entry := decode(t, &buf)
	if got, want := entry["level"], "ERROR"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}
	if got, want := entry["service"], "product-catalog"; got != want {
		t.Errorf("service = %v, want %v", got, want)
	}
	if got, want := entry["timestamp"], fixedTime.Format(time.RFC3339); got != want {
		t.Errorf("timestamp = %v, want %v", got, want)
	}
	fields, ok := entry["fields"].(map[string]interface{})
	if !ok {
		t.Fatalf("fields = %T, want object", entry["fields"])
	}
	if got := fields["err"]; got != "nope" {
		t.Errorf("fields.err = %v, want %q", got, "nope")
	}
}

func TestEmptyFieldsAreOmitted(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug)
	logger.Info(context.Background(), "m", nil)

	entry := decode(t, &buf)
	if _, present := entry["fields"]; present {
		t.Error("fields key present for an entry with no fields")
	}
}

func TestWithFieldsInjectsIntoEveryEntry(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug).WithFields(map[string]interface{}{"trace_id": "t-1"})

	logger.Info(context.Background(), "one", nil)
	buf.Reset()
	logger.Info(context.Background(), "two", map[string]interface{}{"extra": 1})

	fields := decode(t, &buf)["fields"].(map[string]interface{})
	if fields["trace_id"] != "t-1" {
		t.Errorf("fields.trace_id = %v, want %q", fields["trace_id"], "t-1")
	}
	if fields["extra"] != float64(1) {
		t.Errorf("fields.extra = %v, want 1", fields["extra"])
	}
}

func TestErrorf(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newTestLogger(&buf, LevelDebug)
	logger.Errorf(context.Background(), "failed after %d tries", 3)

	entry := decode(t, &buf)
	if got, want := entry["message"], "failed after 3 tries"; got != want {
		t.Errorf("message = %v, want %v", got, want)
	}
	if got, want := entry["level"], "ERROR"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("disk full") }

func TestFailingWriterDoesNotPanic(t *testing.T) {
	t.Parallel()

	logger := NewLoggerWithConfig("svc", Config{Writer: failingWriter{}, Level: LevelDebug})
	logger.Info(context.Background(), "m", nil)
}

func TestAccessors(t *testing.T) {
	t.Parallel()

	logger := newTestLogger(nil, LevelWarn)
	if got := logger.Level(); got != LevelWarn {
		t.Errorf("Level() = %v, want LevelWarn", got)
	}
	if got := logger.Service(); got != "product-catalog" {
		t.Errorf("Service() = %q, want %q", got, "product-catalog")
	}
}

func TestZeroConfigDefaults(t *testing.T) {
	t.Parallel()

	logger := NewLoggerWithConfig("svc", Config{})
	if logger.writer == nil {
		t.Error("writer is nil after a zero Config")
	}
	if logger.clock == nil {
		t.Error("clock is nil after a zero Config")
	}
}
