// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package magiclink implements passwordless sign-in via single-use
// email links. The service issues a token tied to an email address,
// stores its HMAC-SHA256 hash, and validates+consumes it on click.
//
// Delivery is the caller's responsibility — Issue returns the raw token
// that should be embedded in a URL and emailed via [notify].
package magiclink

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
	tokenhash "github.com/golusoris/golusoris/hash"
)

const tokenBytes = 24

// Link is the metadata for a stored magic link.
type Link struct {
	Email     string
	Hash      []byte
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// Store persists magic-link records.
type Store interface {
	Save(ctx context.Context, l Link) error
	// Consume atomically returns and marks one unused link as used.
	// Missing and already-used links return an error.
	Consume(ctx context.Context, hash []byte) (Link, error)
}

// Service issues + verifies magic links.
type Service struct {
	store  Store
	clk    clockwork.Clock
	secret []byte
	ttl    time.Duration
}

// New returns a Service. ttl defaults to 15 minutes if zero. Returns an
// error if store is nil, secret is empty, or ttl is negative.
func New(store Store, clk clockwork.Clock, secret []byte, ttl time.Duration) (*Service, error) {
	if validate.IsNil(store) {
		return nil, errors.New("magiclink: store must not be nil")
	}
	if err := tokenhash.ValidateHMACSHA256Key(secret); err != nil {
		return nil, fmt.Errorf("magiclink: secret: %w", err)
	}
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	if ttl < 0 {
		return nil, errors.New("magiclink: ttl must not be negative")
	}
	return &Service{
		store:  store,
		clk:    clk,
		secret: append([]byte(nil), secret...),
		ttl:    ttl,
	}, nil
}

// Issue creates a single-use link token for email. Returns the raw
// token; embed it in a URL and email it.
func (s *Service) Issue(ctx context.Context, email string) (string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return "", gerr.Validation("magiclink: email required")
	}
	raw, err := randomToken()
	if err != nil {
		return "", err
	}
	hash := tokenhash.HMACSHA256(s.secret, []byte(raw))
	l := Link{Email: email, Hash: hash, ExpiresAt: s.clk.Now().Add(s.ttl)}
	if saveErr := s.store.Save(ctx, l); saveErr != nil {
		return "", fmt.Errorf("magiclink: save: %w", saveErr)
	}
	return raw, nil
}

// Verify consumes a token and returns the email address it was issued
// for. Failures wrap gerr.CodeUnauthorized.
func (s *Service) Verify(ctx context.Context, raw string) (string, error) {
	hash := tokenhash.HMACSHA256(s.secret, []byte(raw))
	l, err := s.store.Consume(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("%w: magiclink: consume: %w", gerr.Unauthorized("invalid magic link"), err)
	}
	if !s.clk.Now().Before(l.ExpiresAt) {
		return "", gerr.Unauthorized("magic link expired")
	}
	if !hmac.Equal(l.Hash, hash) {
		return "", gerr.Unauthorized("magic link mismatch")
	}
	return l.Email, nil
}

func randomToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("magiclink: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// MemoryStore is an in-process store for tests.
type MemoryStore struct {
	mu    sync.Mutex
	links map[string]*Link
	clk   clockwork.Clock
}

// NewMemoryStore returns an initialised in-memory store using the real clock.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(clockwork.NewRealClock())
}

// NewMemoryStoreWithClock returns an initialised in-memory store with an injected clock.
func NewMemoryStoreWithClock(clk clockwork.Clock) *MemoryStore {
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return &MemoryStore{links: map[string]*Link{}, clk: clk}
}

// Save persists a link.
func (m *MemoryStore) Save(_ context.Context, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := l
	cp.Hash = append([]byte(nil), l.Hash...)
	m.links[hashKey(l.Hash)] = &cp
	return nil
}

// Consume atomically returns and marks one unused link as used.
func (m *MemoryStore) Consume(_ context.Context, hash []byte) (Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[hashKey(hash)]
	if !ok {
		return Link{}, errors.New("magiclink: not found")
	}
	if l.UsedAt != nil {
		return Link{}, errors.New("magiclink: already used")
	}
	now := m.clk.Now()
	l.UsedAt = &now
	result := *l
	result.Hash = append([]byte(nil), l.Hash...)
	return result, nil
}

func hashKey(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }
