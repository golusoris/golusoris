package crypto

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/alexedwards/argon2id"

	"github.com/golusoris/golusoris/config"
)

// ErrBusy is returned by [PasswordHasher.TryHash] when the hasher is at its
// concurrency limit. The caller should shed load (e.g. respond HTTP 503).
var ErrBusy = errors.New("crypto: password hasher at capacity")

// PasswordHasher bounds the number of concurrent argon2id hashes. Each hash
// holds ~Memory KiB (64 MiB at the defaults), so an unbounded burst of logins
// is a memory-exhaustion vector; this caps in-flight work and lets callers shed
// load instead of OOMing.
type PasswordHasher struct {
	sem    chan struct{}
	params *argon2id.Params
}

// NewPasswordHasher returns a hasher allowing at most maxConcurrent concurrent
// hashes (values < 1 are clamped to 1). It uses [DefaultPasswordParams].
func NewPasswordHasher(maxConcurrent int) *PasswordHasher {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &PasswordHasher{
		sem:    make(chan struct{}, maxConcurrent),
		params: DefaultPasswordParams,
	}
}

// TryHash hashes plain if a concurrency slot is free, otherwise returns
// [ErrBusy] immediately without blocking — for load-shedding (503) paths.
func (h *PasswordHasher) TryHash(plain string) (string, error) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
		return HashPasswordWith(plain, h.params)
	default:
		return "", ErrBusy
	}
}

// Hash hashes plain, blocking until a slot is free or ctx is done.
func (h *PasswordHasher) Hash(ctx context.Context, plain string) (string, error) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
		return HashPasswordWith(plain, h.params)
	case <-ctx.Done():
		return "", fmt.Errorf("crypto: hash wait: %w", ctx.Err())
	}
}

// newPasswordHasher is the fx provider. Concurrency comes from
// crypto.hasher.max_concurrent, defaulting to GOMAXPROCS.
func newPasswordHasher(cfg *config.Config) *PasswordHasher {
	n := cfg.Int("crypto.hasher.max_concurrent")
	if n < 1 {
		n = runtime.GOMAXPROCS(0)
	}
	return NewPasswordHasher(n)
}
