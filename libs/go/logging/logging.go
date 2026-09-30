package logging

import (
	"context"
	"encoding/json"
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

// ParseLevel maps a wire name back to a Level. Unknown names return LevelInfo
// and false, so that a typo in configuration degrades to a sane default
// instead of silently dropping every message.
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

// contextKey is unexported so that no other package can collide with it.
type contextKey struct{}

// fieldContextKey carries pre-attached log fields through a context.Context.
var fieldContextKey contextKey

// Config configures a Logger.
type Config struct {
	// Writer receives the encoded log entries. Required.
	Writer io.Writer
	// Level is the minimum severity that will be emitted.
	Level Level
	// Service identifies the emitting service.
	Service string
	// Fields are attached to every entry. Optional.
	Fields map[string]interface{}
	// Clock is used for timestamps. Defaults to time.Now; tests override it.
	Clock func() time.Time
}

// DefaultConfig returns a logger configuration that writes INFO and above to
// stdout.
func DefaultConfig() Config {
	return Config{
		Writer:  os.Stdout,
		Level:   LevelInfo,
		Service: "unknown",
		Fields:  map[string]interface{}{},
		Clock:   time.Now,
	}
}

// Logger emits newline-delimited JSON entries.
//
// A Logger is immutable: WithField and WithFields return a new Logger rather
// than mutating the receiver, so a Logger can be shared safely.
type Logger struct {
	writer  io.Writer
	level   Level
	service string
	fields  map[string]interface{}
	clock   func() time.Time
}

// NewLogger creates a logger, filling in defaults for zero-valued Config fields.
func NewLogger(config Config) *Logger {
	if config.Writer == nil {
		config.Writer = os.Stdout
	}
	if config.Service == "" {
		config.Service = "unknown"
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	fields := make(map[string]interface{}, len(config.Fields))
	for k, v := range config.Fields {
		fields[k] = v
	}
	return &Logger{
		writer:  config.Writer,
		level:   config.Level,
		service: config.Service,
		fields:  fields,
		clock:   config.Clock,
	}
}

// WithField returns a copy of the logger with one extra field.
func (l *Logger) WithField(key string, value interface{}) *Logger {
	return l.WithFields(map[string]interface{}{key: value})
}

// WithFields returns a copy of the logger with extra fields merged in. Existing
// keys are overwritten by the new values.
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	merged := make(map[string]interface{}, len(l.fields)+len(fields))
	for k, v := range l.fields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	return &Logger{
		writer:  l.writer,
		level:   l.level,
		service: l.service,
		fields:  merged,
		clock:   l.clock,
	}
}

// WithContext returns a copy of the logger carrying the fields that were
// attached to ctx by ContextWithFields. It is the propagation counterpart of
// the other With* methods.
func (l *Logger) WithContext(ctx context.Context) *Logger {
	if ctx == nil {
		return l
	}
	fields, ok := ctx.Value(fieldContextKey).(map[string]interface{})
	if !ok {
		return l
	}
	return l.WithFields(fields)
}

// Debug logs at LevelDebug.
func (l *Logger) Debug(msg string, fields ...map[string]interface{}) {
	l.log(LevelDebug, msg, fields...)
}

// Info logs at LevelInfo.
func (l *Logger) Info(msg string, fields ...map[string]interface{}) {
	l.log(LevelInfo, msg, fields...)
}

// Warn logs at LevelWarn.
func (l *Logger) Warn(msg string, fields ...map[string]interface{}) {
	l.log(LevelWarn, msg, fields...)
}

// Error logs at LevelError.
func (l *Logger) Error(msg string, fields ...map[string]interface{}) {
	l.log(LevelError, msg, fields...)
}

// Sync flushes the underlying writer when it supports flushing. It is a no-op
// for writers such as os.Stdout, and exists so that callers can defer it
// unconditionally.
func (l *Logger) Sync() error {
	if syncer, ok := l.writer.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	return nil
}

// log writes one entry. Call-site fields override logger-level fields, because
// the call site is the more specific context.
func (l *Logger) log(level Level, msg string, fields ...map[string]interface{}) {
	if level < l.level {
		return
	}

	entry := map[string]interface{}{
		"timestamp": l.clock().UTC().Format(time.RFC3339),
		"level":     level.String(),
		"service":   l.service,
		"message":   msg,
		"fields":    mergeFields(l.fields, fields...),
	}

	// There is nowhere useful to report an encoding failure to: the caller of a
	// log line should not have to handle it. Encoding a map of scalars cannot
	// fail in practice.
	_ = json.NewEncoder(l.writer).Encode(entry)
}

// ContextWithFields returns a context carrying log fields, to be picked up by
// Logger.WithContext.
func ContextWithFields(ctx context.Context, fields map[string]interface{}) context.Context {
	return context.WithValue(ctx, fieldContextKey, fields)
}

func mergeFields(base map[string]interface{}, additional ...map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(base))
	for k, v := range base {
		result[k] = v
	}
	for _, m := range additional {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}
