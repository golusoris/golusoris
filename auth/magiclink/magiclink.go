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
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/errors"
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
	Find(ctx context.Context, hash []byte) (Link, error)
	MarkUsed(ctx context.Context, hash []byte) error
}

// Service issues + verifies magic links.
type Service struct {
	store  Store
	clk    clockwork.Clock
	secret []byte
	ttl    time.Duration
}

// New returns a Service. ttl defaults to 15 minutes if zero. secret must
// be non-empty.
func New(store Store, clk clockwork.Clock, secret []byte, ttl time.Duration) *Service {
	if clk == nil {
		clk = clockwork.NewRealClock()
	}
	if len(secret) == 0 {
		panic("magiclink: secret must not be empty")
	}
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	return &Service{store: store, clk: clk, secret: secret, ttl: ttl}
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
	hash := s.hash(raw)
	l := Link{Email: email, Hash: hash, ExpiresAt: s.clk.Now().Add(s.ttl)}
	if saveErr := s.store.Save(ctx, l); saveErr != nil {
		return "", fmt.Errorf("magiclink: save: %w", saveErr)
	}
	return raw, nil
}

// Verify consumes a token and returns the email address it was issued
// for. Failures wrap gerr.CodeUnauthorized.
func (s *Service) Verify(ctx context.Context, raw string) (string, error) {
	hash := s.hash(raw)
	l, err := s.store.Find(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("%w: magiclink: %w", gerr.Unauthorized("invalid magic link"), err)
	}
	if l.UsedAt != nil {
		return "", gerr.Unauthorized("magic link already used")
	}
	if s.clk.Now().After(l.ExpiresAt) {
		return "", gerr.Unauthorized("magic link expired")
	}
	if !hmac.Equal(l.Hash, hash) {
		return "", gerr.Unauthorized("magic link mismatch")
	}
	if useErr := s.store.MarkUsed(ctx, hash); useErr != nil {
		return "", fmt.Errorf("magiclink: mark used: %w", useErr)
	}
	return l.Email, nil
}

func (s *Service) hash(raw string) []byte {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(raw))
	return h.Sum(nil)
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
	links map[string]*Link
	clk   clockwork.Clock
}

// NewMemoryStore returns an initialised in-memory store using the real clock.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(clockwork.NewRealClock())
}

// NewMemoryStoreWithClock returns an initialised in-memory store with an injected clock.
func NewMemoryStoreWithClock(clk clockwork.Clock) *MemoryStore {
	return &MemoryStore{links: map[string]*Link{}, clk: clk}
}

// Save persists a link.
func (m *MemoryStore) Save(_ context.Context, l Link) error {
	cp := l
	m.links[hashKey(l.Hash)] = &cp
	return nil
}

// Find returns the link or an error if not present.
func (m *MemoryStore) Find(_ context.Context, hash []byte) (Link, error) {
	l, ok := m.links[hashKey(hash)]
	if !ok {
		return Link{}, errors.New("magiclink: not found")
	}
	return *l, nil
}

// MarkUsed sets UsedAt on the matching link.
func (m *MemoryStore) MarkUsed(_ context.Context, hash []byte) error {
	l, ok := m.links[hashKey(hash)]
	if !ok {
		return errors.New("magiclink: not found")
	}
	now := m.clk.Now()
	l.UsedAt = &now
	return nil
}

func hashKey(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }
