// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package apikey issues, revokes, and verifies API keys. Keys are
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
//	svc, err := apikey.New(store, apikey.Options{
//		Prefix: "sk", HMACSecret: secret,
//	})
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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
	tokenhash "github.com/golusoris/golusoris/hash"
)

const (
	rawBytes      = 24 // 24 random bytes → 32-char base64url
	idDigestBytes = 16
	separator     = "_"
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
	// Required. Changing it invalidates every existing raw key because stored
	// digests cannot be re-hashed. Coordinate replacement-key issuance and
	// cutover outside this package.
	HMACSecret []byte
	// Clock is the time source; defaults to clockwork.NewRealClock.
	Clock clockwork.Clock
}

func (o Options) withDefaults() Options {
	if o.Prefix == "" {
		o.Prefix = "key"
	}
	if validate.IsNil(o.Clock) {
		o.Clock = clockwork.NewRealClock()
	}
	return o
}

// Service issues and verifies API keys.
type Service struct {
	store Store
	opts  Options
}

// New returns a Service. Returns an error if store is nil or HMACSecret is empty.
func New(store Store, opts Options) (*Service, error) {
	opts = opts.withDefaults()
	if validate.IsNil(store) {
		return nil, errors.New("apikey: store must not be nil")
	}
	if err := tokenhash.ValidateHMACSHA256Key(opts.HMACSecret); err != nil {
		return nil, fmt.Errorf("apikey: HMACSecret: %w", err)
	}
	opts.HMACSecret = append([]byte(nil), opts.HMACSecret...)
	return &Service{store: store, opts: opts}, nil
}

// Issue creates a new API key for ownerID with the given scopes.
// Returns the raw key (show once), the stored Key metadata, and any
// error. raw must be transmitted to the client and is not recoverable.
func (s *Service) Issue(ctx context.Context, ownerID string, scopes []string) (raw string, key Key, err error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return "", Key{}, gerr.Validation("apikey: ownerID required")
	}
	b := make([]byte, rawBytes)
	if _, err = rand.Read(b); err != nil {
		return "", Key{}, fmt.Errorf("apikey: generate random: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(b)
	raw = s.opts.Prefix + separator + encoded

	id := idFromRaw(s.opts.Prefix, raw)
	hash := s.hash([]byte(raw))

	key = Key{
		ID:        id,
		OwnerID:   ownerID,
		Scopes:    append([]string(nil), scopes...),
		Hash:      hash,
		CreatedAt: s.opts.Clock.Now(),
	}
	if err = s.store.Save(ctx, cloneKey(key)); err != nil {
		return "", Key{}, fmt.Errorf("apikey: save: %w", err)
	}
	return raw, key, nil
}

// Verify validates raw and returns the Key metadata. Returns a wrapped
// gerr.CodeUnauthorized on any failure (missing, revoked, expired,
// hash mismatch) — callers cannot distinguish the reason by design.
func (s *Service) Verify(ctx context.Context, raw string) (Key, error) {
	if !strings.HasPrefix(raw, s.opts.Prefix+separator) {
		return Key{}, gerr.Unauthorized("api key invalid")
	}
	key, err := s.findByRaw(ctx, raw)
	if err != nil {
		return Key{}, fmt.Errorf("%w: apikey: find: %w", gerr.Unauthorized("invalid api key"), err)
	}
	if key.RevokedAt != nil {
		return Key{}, gerr.Unauthorized("api key revoked")
	}
	if key.ExpiresAt != nil && !s.opts.Clock.Now().Before(*key.ExpiresAt) {
		return Key{}, gerr.Unauthorized("api key expired")
	}
	if !hmac.Equal(s.hash([]byte(raw)), key.Hash) {
		return Key{}, gerr.Unauthorized("api key invalid")
	}
	return cloneKey(key), nil
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
	for i := range keys {
		keys[i] = cloneKey(keys[i])
	}
	return keys, nil
}

func (s *Service) hash(raw []byte) []byte {
	h := hmac.New(sha256.New, s.opts.HMACSecret)
	h.Write(raw)
	return h.Sum(nil)
}

// idFromRaw derives a stable 128-bit identifier from the complete raw token.
// Hashing the full token keeps IDs distinct even when prefixes contain separators.
func idFromRaw(prefix, raw string) string {
	h := sha256.Sum256([]byte(raw))
	return prefix + separator + base64.RawURLEncoding.EncodeToString(h[:idDigestBytes])
}

func (s *Service) findByRaw(ctx context.Context, raw string) (Key, error) {
	key, err := s.store.FindByID(ctx, idFromRaw(s.opts.Prefix, raw))
	if err == nil {
		return key, nil
	}
	if !isNotFound(err) {
		return Key{}, fmt.Errorf("current ID: %w", err)
	}
	key, err = s.store.FindByID(ctx, legacyIDFromRaw(raw))
	if err != nil {
		return Key{}, fmt.Errorf("legacy ID: %w", err)
	}
	return key, nil
}

func legacyIDFromRaw(raw string) string {
	parts := strings.SplitN(raw, separator, 2)
	if len(parts) == 2 && len(parts[1]) >= 8 {
		return parts[0] + separator + parts[1][:8]
	}
	h := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(h[:8])
}

func isNotFound(err error) bool {
	var coded *gerr.Error
	return errors.As(err, &coded) && coded.Code == gerr.CodeNotFound
}

func cloneKey(key Key) Key {
	key.Scopes = append([]string(nil), key.Scopes...)
	key.Hash = append([]byte(nil), key.Hash...)
	key.ExpiresAt = cloneTime(key.ExpiresAt)
	key.RevokedAt = cloneTime(key.RevokedAt)
	return key
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
