// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	gerr "github.com/golusoris/golusoris/core/errors"
)

// MaxFileBytes is the largest secret file accepted by [File].
const MaxFileBytes int64 = 64 << 10

// ErrTooLarge reports a secret file larger than [MaxFileBytes].
var ErrTooLarge = errors.New("secrets: file exceeds maximum size")

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

func (envStore) Get(ctx context.Context, key string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
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

func (f fileStore) Get(ctx context.Context, key string) (_ string, err error) {
	if err = contextError(ctx); err != nil {
		return "", err
	}
	if strings.ContainsAny(key, "/\\") {
		return "", fmt.Errorf("secrets: key %q must not contain path separators", key)
	}
	root, err := os.OpenRoot(f.dir)
	if err != nil {
		return "", fmt.Errorf("secrets: open root: %w", err)
	}
	defer gerr.CloseJoin(root, &err, "secrets: close root")
	file, err := root.Open(key)
	if os.IsNotExist(err) {
		return "", ErrNotFound{Key: key}
	}
	if err != nil {
		return "", fmt.Errorf("secrets: open key %q: %w", key, err)
	}
	defer gerr.CloseJoin(file, &err, "secrets: close key "+key)
	data, err := readSecretFile(ctx, file, key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readSecretFile(ctx context.Context, file *os.File, key string) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("secrets: stat key %q: %w", key, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secrets: key %q is not a regular file", key)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("secrets: read key %q: %w", key, err)
	}
	if err = contextError(ctx); err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxFileBytes {
		return nil, fmt.Errorf("%w: key %q", ErrTooLarge, key)
	}
	return data, nil
}

// Static returns a Secret backed by a fixed map. Useful in tests.
func Static(m map[string]string) Secret { return staticStore{m: maps.Clone(m)} }

type staticStore struct{ m map[string]string }

func (s staticStore) Get(ctx context.Context, key string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound{Key: key}
	}
	return v, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("secrets: context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("secrets: get: %w", err)
	}
	return nil
}
