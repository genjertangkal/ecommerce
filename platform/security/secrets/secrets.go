// Package secrets provides the platform's secrets management capability.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Errors returned by this package.
var (
	// ErrNotFound is returned when a path has no stored value.
	ErrNotFound = errors.New("secrets: path not found")
	// ErrUnsupportedBackend is returned when a backend other than "memory" is
	// requested.
	ErrUnsupportedBackend = errors.New("secrets: unsupported backend")
	// ErrInvalidPath is returned for a path that is empty or escapes the root.
	ErrInvalidPath = errors.New("secrets: invalid path")
)

// Backend names understood by this package.
const (
	// BackendMemory keeps secrets in process memory. Intended for tests and
	// single-process tools; it does not survive a restart.
	BackendMemory = "memory"
	// BackendVault is the intended production backend. It is not implemented
	// here; see NewSecretsManager.
	BackendVault = "vault"
	// BackendAWSSecretsManager is the intended AWS backend. Not implemented here.
	BackendAWSSecretsManager = "aws-secretsmanager"
)

// SecretsManager stores and retrieves secrets.
//
// Implementations must be safe for concurrent use.
type SecretsManager interface {
	// GetSecret returns the value stored at path.
	GetSecret(ctx context.Context, path string) (string, error)
	// SetSecret stores value at path, creating or replacing it.
	SetSecret(ctx context.Context, path, value string) error
	// DeleteSecret removes path.
	DeleteSecret(ctx context.Context, path string) error
	// ListSecretPaths returns every known path, sorted.
	ListSecretPaths(ctx context.Context) ([]string, error)
	// RotateSecrets re-keys every managed secret.
	RotateSecrets(ctx context.Context) error
	// AuditLog records an access to a secret.
	AuditLog(ctx context.Context, operation, path string) error
}

// Manager is an in-memory SecretsManager with rotation support.
//
// It exists so that services have a working, testable secrets dependency
// injected from day one. Swapping in a real backend means implementing
// SecretsManager, not changing every call site.
type Manager struct {
	mu             sync.RWMutex
	secrets        map[string]string
	rotateInterval time.Duration
	lastRotation   time.Time
	now            func() time.Time
	audit          []AuditEntry
	generation     int
}

// AuditEntry is a recorded access to a secret.
type AuditEntry struct {
	Operation string
	Path      string
	At        time.Time
}

// NewSecretsManager creates a secrets manager for the named backend.
//
// Only BackendMemory is implemented. Any other backend returns
// ErrUnsupportedBackend rather than a manager that silently accepts writes and
// forgets them, which is how a "working" configuration ends up serving empty
// secrets in production.
func NewSecretsManager(backend string, rotateInterval time.Duration) (*Manager, error) {
	if backend != BackendMemory {
		return nil, fmt.Errorf("%w: %q (implemented: %q)", ErrUnsupportedBackend, backend, BackendMemory)
	}
	if rotateInterval <= 0 {
		rotateInterval = 24 * time.Hour
	}
	return &Manager{
		secrets:        make(map[string]string),
		rotateInterval: rotateInterval,
		now:            time.Now,
	}, nil
}

// SetClock overrides the time source. Intended for tests.
func (m *Manager) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = now
}

// RotationInterval reports the configured rotation interval.
func (m *Manager) RotationInterval() time.Duration { return m.rotateInterval }

// Generation reports how many rotations have happened. Bumped on every
// rotation so callers can invalidate caches holding decrypted values.
func (m *Manager) Generation() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation
}

// validatePath rejects empty paths and any traversal outside the root.
func validatePath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("%w: path is empty", ErrInvalidPath)
	}
	if strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "..") {
		return fmt.Errorf("%w: %q must be a relative path without %q", ErrInvalidPath, path, "..")
	}
	return nil
}

// GetSecret returns the value stored at path.
func (m *Manager) GetSecret(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validatePath(path); err != nil {
		return "", err
	}

	m.mu.RLock()
	value, ok := m.secrets[path]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	return value, nil
}

// SetSecret stores value at path.
func (m *Manager) SetSecret(ctx context.Context, path, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePath(path); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[path] = value
	return nil
}

// DeleteSecret removes path.
func (m *Manager) DeleteSecret(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePath(path); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.secrets[path]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	delete(m.secrets, path)
	return nil
}

// ListSecretPaths returns every stored path, sorted, so that output is stable
// across runs.
func (m *Manager) ListSecretPaths(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	paths := make([]string, 0, len(m.secrets))
	for path := range m.secrets {
		paths = append(paths, path)
	}
	m.mu.RUnlock()

	sort.Strings(paths)
	return paths, nil
}

// RotateSecrets advances the key generation and refreshes the rotation
// timestamp.
//
// An in-memory backend has nothing to re-encrypt, so the meaningful effect is
// the generation bump: callers that cache decrypted values watch it and drop
// them.
func (m *Manager) RotateSecrets(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	m.lastRotation = m.now()
	return nil
}

// LastRotation reports when RotateSecrets last ran.
func (m *Manager) LastRotation() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastRotation
}

// RotationDue reports whether the rotation interval has elapsed.
func (m *Manager) RotationDue() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.lastRotation.IsZero() {
		return true
	}
	return m.now().Sub(m.lastRotation) >= m.rotateInterval
}

// AuditLog records an access to a secret.
//
// Auditing is in-memory and unbounded here; a production backend must forward
// the record to a durable, tamper-evident sink.
func (m *Manager) AuditLog(ctx context.Context, operation, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if operation == "" {
		return fmt.Errorf("%w: operation is empty", ErrInvalidPath)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, AuditEntry{
		Operation: operation,
		Path:      path,
		At:        m.now(),
	})
	return nil
}

// AuditEntries returns a copy of the audit trail.
func (m *Manager) AuditEntries() []AuditEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AuditEntry, len(m.audit))
	copy(out, m.audit)
	return out
}

// Ensure Manager satisfies the interface.
var _ SecretsManager = (*Manager)(nil)
