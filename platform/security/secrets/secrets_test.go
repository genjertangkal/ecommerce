package secrets

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewSecretsManager(BackendMemory, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSecretsManager() error = %v", err)
	}
	m.SetClock(func() time.Time { return fixedTime })
	return m
}

// The previous implementation accepted every backend and returned ("", nil)
// from every method, so a service configured for Vault happily read empty
// secrets forever.
func TestUnsupportedBackendFailsLoudly(t *testing.T) {
	t.Parallel()

	for _, backend := range []string{BackendVault, BackendAWSSecretsManager, "consul", ""} {
		m, err := NewSecretsManager(backend, time.Hour)
		if !errors.Is(err, ErrUnsupportedBackend) {
			t.Errorf("NewSecretsManager(%q) error = %v, want ErrUnsupportedBackend", backend, err)
		}
		if m != nil {
			t.Errorf("NewSecretsManager(%q) returned a non-nil manager alongside the error", backend)
		}
	}
}

func TestSetAndGet(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	if err := m.SetSecret(ctx, "db/password", "s3cr3t"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}

	got, err := m.GetSecret(ctx, "db/password")
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if got != "s3cr3t" {
		t.Errorf("GetSecret() = %q, want %q", got, "s3cr3t")
	}
}

func TestGetMissingSecret(t *testing.T) {
	t.Parallel()

	_, err := newManager(t).GetSecret(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSecret() error = %v, want ErrNotFound", err)
	}
}

func TestSetReplaces(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	_ = m.SetSecret(ctx, "k", "v1")
	_ = m.SetSecret(ctx, "k", "v2")

	got, err := m.GetSecret(ctx, "k")
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if got != "v2" {
		t.Errorf("GetSecret() = %q, want %q", got, "v2")
	}
}

func TestPathValidation(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	bad := []string{"", "   ", "/absolute/path", "db/../../etc/passwd"}
	for _, path := range bad {
		if err := m.SetSecret(ctx, path, "v"); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("SetSecret(%q) error = %v, want ErrInvalidPath", path, err)
		}
		if _, err := m.GetSecret(ctx, path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("GetSecret(%q) error = %v, want ErrInvalidPath", path, err)
		}
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	_ = m.SetSecret(ctx, "k", "v")
	if err := m.DeleteSecret(ctx, "k"); err != nil {
		t.Fatalf("DeleteSecret() error = %v", err)
	}
	if _, err := m.GetSecret(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSecret() after delete error = %v, want ErrNotFound", err)
	}
	if err := m.DeleteSecret(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteSecret() on a missing key error = %v, want ErrNotFound", err)
	}
}

func TestListSecretPathsIsSorted(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	for _, p := range []string{"z/last", "a/first", "m/middle"} {
		_ = m.SetSecret(ctx, p, "v")
	}

	got, err := m.ListSecretPaths(ctx)
	if err != nil {
		t.Fatalf("ListSecretPaths() error = %v", err)
	}
	want := []string{"a/first", "m/middle", "z/last"}
	if len(got) != len(want) {
		t.Fatalf("ListSecretPaths() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListSecretPaths()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCancelledContextIsRespected(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := m.SetSecret(ctx, "k", "v"); !errors.Is(err, context.Canceled) {
		t.Errorf("SetSecret() error = %v, want context.Canceled", err)
	}
	if _, err := m.GetSecret(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("GetSecret() error = %v, want context.Canceled", err)
	}
	if err := m.RotateSecrets(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("RotateSecrets() error = %v, want context.Canceled", err)
	}
}

func TestRotationBumpsGeneration(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	if got := m.Generation(); got != 0 {
		t.Errorf("Generation() before any rotation = %d, want 0", got)
	}
	if err := m.RotateSecrets(ctx); err != nil {
		t.Fatalf("RotateSecrets() error = %v", err)
	}
	if got := m.Generation(); got != 1 {
		t.Errorf("Generation() = %d, want 1", got)
	}
	if got := m.LastRotation(); !got.Equal(fixedTime) {
		t.Errorf("LastRotation() = %v, want %v", got, fixedTime)
	}
}

func TestRotationDue(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	// Never rotated: due immediately.
	if !m.RotationDue() {
		t.Error("RotationDue() = false before the first rotation, want true")
	}

	if err := m.RotateSecrets(ctx); err != nil {
		t.Fatalf("RotateSecrets() error = %v", err)
	}
	if m.RotationDue() {
		t.Error("RotationDue() = true immediately after rotating, want false")
	}
}

func TestNonPositiveIntervalIsNormalised(t *testing.T) {
	t.Parallel()

	m, err := NewSecretsManager(BackendMemory, 0)
	if err != nil {
		t.Fatalf("NewSecretsManager() error = %v", err)
	}
	if m.RotationInterval() != 24*time.Hour {
		t.Errorf("RotationInterval() = %v, want the 24h default", m.RotationInterval())
	}
}

func TestAuditLog(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	if err := m.AuditLog(ctx, "read", "db/password"); err != nil {
		t.Fatalf("AuditLog() error = %v", err)
	}
	if err := m.AuditLog(ctx, "", "db/password"); err == nil {
		t.Error("AuditLog() with an empty operation = nil error, want an error")
	}

	entries := m.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("AuditEntries() has %d entries, want 1", len(entries))
	}
	if entries[0].Operation != "read" || entries[0].Path != "db/password" {
		t.Errorf("entry = %+v, want read/db/password", entries[0])
	}
	if !entries[0].At.Equal(fixedTime) {
		t.Errorf("entry.At = %v, want %v", entries[0].At, fixedTime)
	}
}

func TestAuditEntriesIsACopy(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	_ = m.AuditLog(context.Background(), "read", "k")

	entries := m.AuditEntries()
	entries[0].Operation = "tampered"

	if got := m.AuditEntries()[0].Operation; got != "read" {
		t.Errorf("AuditEntries() leaked internal state: got %q, want %q", got, "read")
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "concurrent"
			_ = m.SetSecret(ctx, path, "v")
			_, _ = m.GetSecret(ctx, path)
			_, _ = m.ListSecretPaths(ctx)
			_ = m.AuditLog(ctx, "read", path)
		}(i)
	}
	wg.Wait()

	if _, err := m.GetSecret(ctx, "concurrent"); err != nil {
		t.Errorf("GetSecret() after concurrent writes error = %v", err)
	}
}

func TestSetClockIgnoresNil(t *testing.T) {
	t.Parallel()

	m := newManager(t)
	m.SetClock(nil)

	if err := m.RotateSecrets(context.Background()); err != nil {
		t.Fatalf("RotateSecrets() error = %v", err)
	}
	if m.LastRotation().IsZero() {
		t.Error("LastRotation() is zero: the nil clock replaced the default")
	}
}
