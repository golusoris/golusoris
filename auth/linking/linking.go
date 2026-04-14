// Package linking maps external identity-provider subjects (OIDC,
// OAuth) to local user IDs. A single user can have multiple linked
// identities (e.g. Google + GitHub).
//
// Storage is pluggable via [Store].
package linking

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	gerr "github.com/golusoris/golusoris/errors"
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
	Save(ctx context.Context, i Identity) error
	Find(ctx context.Context, provider, subject string) (Identity, error)
	ListForUser(ctx context.Context, userID string) ([]Identity, error)
	Delete(ctx context.Context, provider, subject string) error
}

// Service manages identity links.
type Service struct{ store Store }

// New returns a Service.
func New(store Store) *Service { return &Service{store: store} }

// Link associates (provider,subject) with userID. If the link already
// exists for a different user, returns gerr.CodeConflict.
func (s *Service) Link(ctx context.Context, userID, provider, subject, email string) error {
	existing, err := s.store.Find(ctx, provider, subject)
	if err == nil {
		if existing.UserID != userID {
			return gerr.Conflict("identity already linked to another user")
		}
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("linking: find: %w", err)
	}
	i := Identity{Provider: provider, Subject: subject, UserID: userID, Email: email, CreatedAt: time.Now()}
	if saveErr := s.store.Save(ctx, i); saveErr != nil {
		return fmt.Errorf("linking: save: %w", saveErr)
	}
	return nil
}

// Lookup returns the local UserID for an external identity.
func (s *Service) Lookup(ctx context.Context, provider, subject string) (string, error) {
	i, err := s.store.Find(ctx, provider, subject)
	if err != nil {
		return "", fmt.Errorf("linking: lookup: %w", err)
	}
	return i.UserID, nil
}

// List returns all identities linked to userID.
func (s *Service) List(ctx context.Context, userID string) ([]Identity, error) {
	out, err := s.store.ListForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("linking: list: %w", err)
	}
	return out, nil
}

// Unlink removes an identity association.
func (s *Service) Unlink(ctx context.Context, provider, subject string) error {
	if err := s.store.Delete(ctx, provider, subject); err != nil {
		return fmt.Errorf("linking: unlink: %w", err)
	}
	return nil
}

func isNotFound(err error) bool {
	var e *gerr.Error
	return errors.As(err, &e) && e.Code == gerr.CodeNotFound
}

// MemoryStore is an in-process Store for tests.
type MemoryStore struct {
	mu sync.Mutex
	m  map[string]Identity // key: provider+"|"+subject
}

// NewMemoryStore returns an initialised store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]Identity{}} }

// Save inserts or updates an identity.
func (s *MemoryStore) Save(_ context.Context, i Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key(i.Provider, i.Subject)] = i
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

// Delete removes the identity if present.
func (s *MemoryStore) Delete(_ context.Context, p, sub string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key(p, sub))
	return nil
}

func key(p, sub string) string { return p + "|" + sub }
