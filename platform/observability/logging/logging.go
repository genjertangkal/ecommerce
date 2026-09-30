// Package logging provides the platform's structured logging capability.
package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Level is a log severity threshold.
type Level int

const (
	// LevelDebug is the debug log level.
	LevelDebug Level = iota
	// LevelInfo is the info log level.
	LevelInfo
	// LevelWarn is the warning log level.
	LevelWarn
	// LevelError is the error log level.
	LevelError
)

// String returns the canonical wire name of the level.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel maps a wire name to a Level.
func ParseLevel(name string) (Level, bool) {
	switch name {
	case "DEBUG", "debug":
		return LevelDebug, true
	case "INFO", "info":
		return LevelInfo, true
	case "WARN", "warn", "WARNING", "warning":
		return LevelWarn, true
	case "ERROR", "error":
		return LevelError, true
	default:
		return LevelInfo, false
	}
}

// Config configures a Logger.
type Config struct {
	// Writer receives encoded entries. Defaults to os.Stdout.
	Writer io.Writer
	// Level is the minimum severity emitted. Defaults to LevelInfo.
	Level Level
	// Clock supplies timestamps. Defaults to time.Now.
	Clock func() time.Time
}

// Logger emits newline-delimited JSON entries tagged with a service name.
//
// Unlike //libs/go/logging, this capability takes an explicit io.Writer and a
// context on every call, so that a request-scoped writer can be honoured without
// the caller re-deriving a logger per request.
type Logger struct {
	writer  io.Writer
	level   Level
	service string
	clock   func() time.Time
}

// NewLogger creates a Logger for the named service.
func NewLogger(serviceName string, level Level) *Logger {
	return NewLoggerWithConfig(serviceName, Config{Level: level})
}

// NewLoggerWithConfig creates a Logger, filling in zero-valued Config fields.
func NewLoggerWithConfig(serviceName string, cfg Config) *Logger {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Logger{
		writer:  cfg.Writer,
		level:   cfg.Level,
		service: serviceName,
		clock:   cfg.Clock,
	}
}

// Level reports the configured severity threshold.
func (l *Logger) Level() Level { return l.level }

// Service reports the service name attached to every entry.
func (l *Logger) Service() string { return l.service }

// Debug logs at LevelDebug.
func (l *Logger) Debug(ctx context.Context, msg string, fields map[string]interface{}) {
	l.log(ctx, LevelDebug, msg, fields)
}

// Info logs at LevelInfo.
func (l *Logger) Info(ctx context.Context, msg string, fields map[string]interface{}) {
	l.log(ctx, LevelInfo, msg, fields)
}

// Warn logs at LevelWarn.
func (l *Logger) Warn(ctx context.Context, msg string, fields map[string]interface{}) {
	l.log(ctx, LevelWarn, msg, fields)
}

// Error logs at LevelError.
func (l *Logger) Error(ctx context.Context, msg string, fields map[string]interface{}) {
	l.log(ctx, LevelError, msg, fields)
}

// Errorf logs at LevelError with a formatted message.
func (l *Logger) Errorf(ctx context.Context, format string, args ...interface{}) {
	l.log(ctx, LevelError, fmt.Sprintf(format, args...), nil)
}

func (l *Logger) log(ctx context.Context, level Level, msg string, fields map[string]interface{}) {
	if level < l.level {
		return
	}

	entry := map[string]interface{}{
		"timestamp": l.clock().UTC().Format(time.RFC3339),
		"level":     level.String(),
		"service":   l.service,
		"message":   msg,
	}
	if len(fields) > 0 {
		entry["fields"] = fields
	}

	// A failure to write a log line must not take the request down, so the
	// error is deliberately dropped.
	_ = json.NewEncoder(l.writer).Encode(entry)
}

// WithFields returns a copy of the logger with fields merged into every entry.
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	base := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		base[k] = v
	}
	return &Logger{
		writer:  &fieldWriter{inner: l.writer, fields: base},
		level:   l.level,
		service: l.service,
		clock:   l.clock,
	}
}

// fieldWriter injects a fixed set of fields into every encoded entry.
type fieldWriter struct {
	inner  io.Writer
	fields map[string]interface{}
}

func (w *fieldWriter) Write(p []byte) (int, error) {
	var entry map[string]interface{}
	if err := json.Unmarshal(p, &entry); err != nil {
		// Not a JSON entry; pass it through untouched rather than corrupting it.
		return w.inner.Write(p)
	}
	existing, _ := entry["fields"].(map[string]interface{})
	merged := make(map[string]interface{}, len(w.fields)+len(existing))
	for k, v := range w.fields {
		merged[k] = v
	}
	for k, v := range existing {
		merged[k] = v
	}
	entry["fields"] = merged

	out, err := json.Marshal(entry)
	if err != nil {
		return w.inner.Write(p)
	}
	out = append(out, '\n')
	return w.inner.Write(out)
}
