// Package secrets provides a pluggable Secret interface with env-var and
// file-based backends. Apps that need HashiCorp Vault, AWS Secrets Manager,
// GCP Secret Manager, or Azure Key Vault can wrap this interface without
// changing call sites.
//
// Usage:
//
//	s := secrets.Env()              // reads from os.Getenv
//	val, err := s.Get(ctx, "DB_PASSWORD")
//
//	s2 := secrets.File("/run/secrets") // reads files named by key
//	val, err := s2.Get(ctx, "db_password")
package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Secret is the minimal interface for secret retrieval.
// Implementations must be safe for concurrent use.
type Secret interface {
	// Get returns the secret value for key, or an error when not found.
	Get(ctx context.Context, key string) (string, error)
}

// ErrNotFound is returned when a key is absent from the backend.
type ErrNotFound struct{ Key string }

func (e ErrNotFound) Error() string { return fmt.Sprintf("secrets: key %q not found", e.Key) }

// envStore reads secrets from environment variables.
type envStore struct{}

// Env returns a Secret that reads values from os.Getenv.
// Key lookup is exact-match and case-sensitive.
func Env() Secret { return envStore{} }

func (envStore) Get(_ context.Context, key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return "", ErrNotFound{Key: key}
	}
	return v, nil
}

// fileStore reads secrets from files in a directory.
// Each file name is the key; the file content is the value.
type fileStore struct{ dir string }

// File returns a Secret that reads values from files under dir.
// The key is used as the file name (path separators are rejected).
// Leading/trailing whitespace is trimmed from file contents.
func File(dir string) Secret { return fileStore{dir: dir} }

func (f fileStore) Get(_ context.Context, key string) (string, error) {
	if strings.ContainsAny(key, "/\\") {
		return "", fmt.Errorf("secrets: key %q must not contain path separators", key)
	}
	path := filepath.Join(f.dir, key)
	data, err := os.ReadFile(path) //nolint:gosec // G304: secret file path is operator-controlled config
	if os.IsNotExist(err) {
		return "", ErrNotFound{Key: key}
	}
	if err != nil {
		return "", fmt.Errorf("secrets: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Static returns a Secret backed by a fixed map. Useful in tests.
func Static(m map[string]string) Secret { return staticStore{m: m} }

type staticStore struct{ m map[string]string }

func (s staticStore) Get(_ context.Context, key string) (string, error) {
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound{Key: key}
	}
	return v, nil
}
