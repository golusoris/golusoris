// Package apikey issues, rotates, and verifies API keys. Keys are
// stored as HMAC-SHA256 hashes (never plaintext). The raw key is
// returned only at creation time.
//
// Storage is intentionally left to the caller: provide a [Store]
// implementation backed by Postgres, Redis, or any other store.
//
// Key format: "<prefix>_<random-base62-32-chars>"
// Example:    "sk_X7kLmN3pQ9rSvW2yZaB4cD6eF8gH0jK"
//
// Usage:
//
//	svc := apikey.New(store, apikey.Options{Prefix: "sk"})
//
//	raw, key, err := svc.Issue(ctx, "user-123", []string{"read"})
//	// store raw — it's never retrievable again
//
//	key, err := svc.Verify(ctx, rawFromHeader)
package apikey

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	gerr "github.com/golusoris/golusoris/errors"
)

const (
	rawBytes  = 24  // 24 random bytes → 32-char base64url
	separator = "_"
)

// Key holds the metadata stored in the backing store.
type Key struct {
	ID        string
	OwnerID   string
	Scopes    []string
	Hash      []byte // HMAC-SHA256 of the raw key
	CreatedAt time.Time
	ExpiresAt *time.Time // nil = never
	RevokedAt *time.Time
}

// Store is the backing store contract. Implementations are provided by
// the app (e.g. a sqlc-generated Postgres repo).
type Store interface {
	// Save persists a new key record. ID must be unique.
	Save(ctx context.Context, k Key) error
	// FindByID returns the key or gerr.CodeNotFound.
	FindByID(ctx context.Context, id string) (Key, error)
	// Revoke marks a key revoked. Returns gerr.CodeNotFound if not found.
	Revoke(ctx context.Context, id string) error
	// ListByOwner returns all non-revoked keys for ownerID.
	ListByOwner(ctx context.Context, ownerID string) ([]Key, error)
}

// Options tunes the service.
type Options struct {
	// Prefix is prepended to every raw key (e.g. "sk" → "sk_…").
	// Default "key".
	Prefix string
	// HMACSecret is the secret used to hash keys before storage.
	// Required. Rotate by re-hashing all existing hashes with the new
	// secret (provide a migration path).
	HMACSecret []byte
}

func (o Options) withDefaults() Options {
	if o.Prefix == "" {
		o.Prefix = "key"
	}
	return o
}

// Service issues and verifies API keys.
type Service struct {
	store Store
	opts  Options
}

// New returns a Service. Panics if HMACSecret is empty.
func New(store Store, opts Options) *Service {
	opts = opts.withDefaults()
	if len(opts.HMACSecret) == 0 {
		panic("apikey: HMACSecret must not be empty")
	}
	return &Service{store: store, opts: opts}
}

// Issue creates a new API key for ownerID with the given scopes.
// Returns the raw key (show once), the stored Key metadata, and any
// error. raw must be transmitted to the client and is not recoverable.
func (s *Service) Issue(ctx context.Context, ownerID string, scopes []string) (raw string, key Key, err error) {
	b := make([]byte, rawBytes)
	if _, err = rand.Read(b); err != nil {
		return "", Key{}, fmt.Errorf("apikey: generate random: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(b)
	raw = s.opts.Prefix + separator + encoded

	id := idFromRaw(raw)
	hash := s.hash([]byte(raw))

	key = Key{
		ID:        id,
		OwnerID:   ownerID,
		Scopes:    scopes,
		Hash:      hash,
		CreatedAt: time.Now(),
	}
	if err = s.store.Save(ctx, key); err != nil {
		return "", Key{}, fmt.Errorf("apikey: save: %w", err)
	}
	return raw, key, nil
}

// Verify validates raw and returns the Key metadata. Returns a wrapped
// gerr.CodeUnauthorized on any failure (missing, revoked, expired,
// hash mismatch) — callers cannot distinguish the reason by design.
func (s *Service) Verify(ctx context.Context, raw string) (Key, error) {
	id := idFromRaw(raw)
	key, err := s.store.FindByID(ctx, id)
	if err != nil {
		return Key{}, fmt.Errorf("%w: apikey: find: %w", gerr.Unauthorized("invalid api key"), err)
	}
	if key.RevokedAt != nil {
		return Key{}, gerr.Unauthorized("api key revoked")
	}
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return Key{}, gerr.Unauthorized("api key expired")
	}
	if !hmac.Equal(s.hash([]byte(raw)), key.Hash) {
		return Key{}, gerr.Unauthorized("api key invalid")
	}
	return key, nil
}

// Revoke marks the key with id as revoked.
func (s *Service) Revoke(ctx context.Context, id string) error {
	if err := s.store.Revoke(ctx, id); err != nil {
		return fmt.Errorf("apikey: revoke: %w", err)
	}
	return nil
}

// ListByOwner returns all active keys for ownerID.
func (s *Service) ListByOwner(ctx context.Context, ownerID string) ([]Key, error) {
	keys, err := s.store.ListByOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("apikey: list: %w", err)
	}
	return keys, nil
}

func (s *Service) hash(raw []byte) []byte {
	h := hmac.New(sha256.New, s.opts.HMACSecret)
	h.Write(raw)
	return h.Sum(nil)
}

// idFromRaw derives a stable, safe key ID from the raw token.
// Uses the prefix + first 8 chars of the encoded part so the ID
// is human-readable in logs without leaking the full key.
func idFromRaw(raw string) string {
	parts := strings.SplitN(raw, separator, 2)
	if len(parts) == 2 && len(parts[1]) >= 8 {
		return parts[0] + separator + parts[1][:8]
	}
	// Fallback: hash the whole raw value.
	h := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(h[:8])
}
