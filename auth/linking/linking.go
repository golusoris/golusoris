// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package linking maps external identity-provider subjects (OIDC,
// OAuth) to local user IDs. A single user can have multiple linked
// identities (e.g. Google + GitHub).
//
// Storage is pluggable via [Store]. Identity ownership is established through
// one atomic [Store.Claim] operation.
package linking

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
)

// Identity is the (provider, subject) pair issued by an external IdP,
// linked to a local UserID.
type Identity struct {
	Provider  string
	Subject   string
	UserID    string
	Email     string
	CreatedAt time.Time
}

// Store persists identity links.
type Store interface {
	// Claim atomically creates an identity when absent and returns the
	// authoritative stored identity. It must never replace an existing owner.
	Claim(ctx context.Context, i Identity) (Identity, error)
	Find(ctx context.Context, provider, subject string) (Identity, error)
	ListForUser(ctx context.Context, userID string) ([]Identity, error)
	// DeleteOwned atomically removes an identity only when userID owns it.
	DeleteOwned(ctx context.Context, userID, provider, subject string) error
}

// Service manages identity links.
type Service struct {
	store Store
	clk   clockwork.Clock
}

// New returns a Service using the real clock.
func New(store Store) (*Service, error) { return NewWithClock(store, clockwork.NewRealClock()) }

// NewWithClock returns a Service with an injected clock.
func NewWithClock(store Store, clk clockwork.Clock) (*Service, error) {
	if validate.IsNil(store) {
		return nil, errors.New("linking: store must not be nil")
	}
	if validate.IsNil(clk) {
		return nil, errors.New("linking: clock must not be nil")
	}
	return &Service{store: store, clk: clk}, nil
}

// Link associates (provider,subject) with userID. If the link already
// exists for a different user, returns gerr.CodeConflict.
func (s *Service) Link(ctx context.Context, userID, provider, subject, email string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(provider) == "" || strings.TrimSpace(subject) == "" {
		return gerr.Validation("linking: userID, provider, and subject are required")
	}
	i := Identity{Provider: provider, Subject: subject, UserID: userID, Email: email, CreatedAt: s.clk.Now()}
	claimed, err := s.store.Claim(ctx, i)
	if err != nil {
		return fmt.Errorf("linking: claim: %w", err)
	}
	if claimed.UserID != userID {
		return gerr.Conflict("identity already linked to another user")
	}
	return nil
}

// Lookup returns the local UserID for an external identity.
func (s *Service) Lookup(ctx context.Context, provider, subject string) (string, error) {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(subject) == "" {
		return "", gerr.Validation("linking: provider and subject are required")
	}
	i, err := s.store.Find(ctx, provider, subject)
	if err != nil {
		return "", fmt.Errorf("linking: lookup: %w", err)
	}
	return i.UserID, nil
}

// List returns all identities linked to userID.
func (s *Service) List(ctx context.Context, userID string) ([]Identity, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, gerr.Validation("linking: userID required")
	}
	out, err := s.store.ListForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("linking: list: %w", err)
	}
	return out, nil
}

// Unlink removes an identity association only when userID owns it.
func (s *Service) Unlink(ctx context.Context, userID, provider, subject string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(provider) == "" || strings.TrimSpace(subject) == "" {
		return gerr.Validation("linking: userID, provider, and subject are required")
	}
	if err := s.store.DeleteOwned(ctx, userID, provider, subject); err != nil {
		return fmt.Errorf("linking: unlink: %w", err)
	}
	return nil
}

// MemoryStore is an in-process Store for tests.
type MemoryStore struct {
	mu sync.Mutex
	m  map[identityKey]Identity
}

type identityKey struct {
	provider string
	subject  string
}

// NewMemoryStore returns an initialised store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[identityKey]Identity{}} }

// Claim atomically inserts an identity when absent and returns its owner.
func (s *MemoryStore) Claim(_ context.Context, i Identity) (Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	storedKey := key(i.Provider, i.Subject)
	if existing, ok := s.m[storedKey]; ok {
		return existing, nil
	}
	s.m[storedKey] = i
	return i, nil
}

// Save safely claims an identity without replacing an existing owner.
//
// Deprecated: implement [Store.Claim] and call [Service.Link].
func (s *MemoryStore) Save(ctx context.Context, i Identity) error {
	claimed, err := s.Claim(ctx, i)
	if err != nil {
		return err
	}
	if claimed.UserID != i.UserID {
		return gerr.Conflict("identity already linked to another user")
	}
	return nil
}

// Find returns the identity or gerr.NotFound.
func (s *MemoryStore) Find(_ context.Context, p, sub string) (Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.m[key(p, sub)]
	if !ok {
		return Identity{}, gerr.NotFound("linking: not found")
	}
	return i, nil
}

// ListForUser returns all identities for userID.
func (s *MemoryStore) ListForUser(_ context.Context, userID string) ([]Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Identity, 0)
	for _, v := range s.m {
		if v.UserID == userID {
			out = append(out, v)
		}
	}
	return out, nil
}

// DeleteOwned removes the identity only when userID owns it.
func (s *MemoryStore) DeleteOwned(_ context.Context, userID, p, sub string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	storedKey := key(p, sub)
	identity, ok := s.m[storedKey]
	if !ok {
		return gerr.NotFound("linking: not found")
	}
	if identity.UserID != userID {
		return gerr.Conflict("linking: identity owned by another user")
	}
	delete(s.m, storedKey)
	return nil
}

func key(provider, subject string) identityKey {
	return identityKey{provider: provider, subject: subject}
}
