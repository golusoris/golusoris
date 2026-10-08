// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package linking_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/linking"
	gerr "github.com/golusoris/golusoris/core/errors"
)

func TestService_LinkLookup(t *testing.T) {
	t.Parallel()

	svc := newService(t, linking.NewMemoryStore())

	require.NoError(t, svc.Link(context.Background(), "u-1", "google", "g-123", "u@x"))

	uid, err := svc.Lookup(context.Background(), "google", "g-123")
	require.NoError(t, err)
	require.Equal(t, "u-1", uid)
}

func TestService_LinkConflict(t *testing.T) {
	t.Parallel()

	svc := newService(t, linking.NewMemoryStore())
	ctx := context.Background()

	require.NoError(t, svc.Link(ctx, "u-1", "github", "gh-1", ""))
	err := svc.Link(ctx, "u-2", "github", "gh-1", "")
	require.Error(t, err)
}

func TestMemoryStore_IdentityKeyHasNoDelimiterCollision(t *testing.T) {
	t.Parallel()

	svc := newService(t, linking.NewMemoryStore())
	ctx := t.Context()
	require.NoError(t, svc.Link(ctx, "u-1", "provider|tenant", "subject", ""))
	require.NoError(t, svc.Link(ctx, "u-2", "provider", "tenant|subject", ""))

	first, err := svc.Lookup(ctx, "provider|tenant", "subject")
	require.NoError(t, err)
	second, err := svc.Lookup(ctx, "provider", "tenant|subject")
	require.NoError(t, err)
	require.Equal(t, "u-1", first)
	require.Equal(t, "u-2", second)
}

func TestServicePreservesOpaqueIdentityBytes(t *testing.T) {
	t.Parallel()

	service := newService(t, linking.NewMemoryStore())
	ctx := t.Context()
	require.NoError(t, service.Link(ctx, " user ", " oidc ", " subject ", ""))
	require.NoError(t, service.Link(ctx, "plain", "oidc", "subject", ""))

	userID, err := service.Lookup(ctx, " oidc ", " subject ")
	require.NoError(t, err)
	require.Equal(t, " user ", userID)
	plain, err := service.Lookup(ctx, "oidc", "subject")
	require.NoError(t, err)
	require.Equal(t, "plain", plain)

	identities, err := service.List(ctx, " user ")
	require.NoError(t, err)
	require.Len(t, identities, 1)
	require.NoError(t, service.Unlink(ctx, " user ", " oidc ", " subject "))
}

func TestService_ListAndUnlink(t *testing.T) {
	t.Parallel()

	svc := newService(t, linking.NewMemoryStore())
	ctx := context.Background()
	require.NoError(t, svc.Link(ctx, "u-3", "google", "g-x", "x@y"))
	require.NoError(t, svc.Link(ctx, "u-3", "github", "gh-x", "x@y"))

	list, err := svc.List(ctx, "u-3")
	require.NoError(t, err)
	require.Len(t, list, 2)

	require.Error(t, svc.Unlink(ctx, "u-other", "google", "g-x"))
	require.NoError(t, svc.Unlink(ctx, "u-3", "google", "g-x"))

	list, err = svc.List(ctx, "u-3")
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestService_ConcurrentLinkClaimsOneOwner(t *testing.T) {
	t.Parallel()

	store := newRaceRegressionStore()
	svc := newService(t, store)
	start := make(chan struct{})
	results := make(chan linkResult, 2)

	for _, userID := range []string{"u-1", "u-2"} {
		go func() {
			<-start
			err := svc.Link(t.Context(), userID, "oidc", "subject", userID+"@example.test")
			results <- linkResult{userID: userID, err: err}
		}()
	}
	close(start)

	first := <-results
	second := <-results
	assertSingleOwner(t, store, first, second)
}

type linkResult struct {
	userID string
	err    error
}

type raceRegressionStore struct {
	mu          sync.Mutex
	identities  map[string]linking.Identity
	findBarrier sync.WaitGroup
}

func newRaceRegressionStore() *raceRegressionStore {
	s := &raceRegressionStore{identities: make(map[string]linking.Identity)}
	s.findBarrier.Add(2)
	return s
}

func (s *raceRegressionStore) Claim(_ context.Context, identity linking.Identity) (linking.Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identityKey := identity.Provider + "|" + identity.Subject
	if existing, ok := s.identities[identityKey]; ok {
		return existing, nil
	}
	s.identities[identityKey] = identity
	return identity, nil
}

// Find holds both callers after their reads so the former Find-then-Save
// implementation deterministically observes absence twice before either save.
func (s *raceRegressionStore) Find(_ context.Context, provider, subject string) (linking.Identity, error) {
	s.mu.Lock()
	identity, ok := s.identities[provider+"|"+subject]
	s.mu.Unlock()
	s.findBarrier.Done()
	s.findBarrier.Wait()
	if !ok {
		return linking.Identity{}, gerr.NotFound("not found")
	}
	return identity, nil
}

func (s *raceRegressionStore) Save(_ context.Context, identity linking.Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[identity.Provider+"|"+identity.Subject] = identity
	return nil
}

func (s *raceRegressionStore) ListForUser(_ context.Context, _ string) ([]linking.Identity, error) {
	return nil, nil
}

func (s *raceRegressionStore) DeleteOwned(_ context.Context, _, _, _ string) error { return nil }

func TestServiceRejectsInvalidConstructionAndIdentity(t *testing.T) {
	t.Parallel()
	_, err := linking.New(nil)
	require.Error(t, err)
	_, err = linking.NewWithClock(linking.NewMemoryStore(), nil)
	require.Error(t, err)

	svc := newService(t, linking.NewMemoryStore())
	for _, values := range [][3]string{
		{"", "provider", "subject"},
		{"user", "", "subject"},
		{"user", "provider", ""},
	} {
		require.Error(t, svc.Link(t.Context(), values[0], values[1], values[2], ""))
	}
}

func TestServiceRejectsTypedNilDependencies(t *testing.T) {
	t.Parallel()
	var store *raceRegressionStore
	_, err := linking.New(store)
	require.Error(t, err)

	var clk *clockwork.FakeClock
	_, err = linking.NewWithClock(linking.NewMemoryStore(), clk)
	require.Error(t, err)
}

func newService(t *testing.T, store linking.Store) *linking.Service {
	t.Helper()
	service, err := linking.New(store)
	require.NoError(t, err)
	return service
}

func assertSingleOwner(t *testing.T, store *raceRegressionStore, results ...linkResult) {
	t.Helper()
	successes := make([]linkResult, 0, 1)
	conflicts := 0
	for _, result := range results {
		if result.err == nil {
			successes = append(successes, result)
			continue
		}
		var typed *gerr.Error
		require.True(t, errors.As(result.err, &typed))
		require.Equal(t, gerr.CodeConflict, typed.Code)
		conflicts++
	}
	require.Len(t, successes, 1)
	require.Equal(t, 1, conflicts)

	store.mu.Lock()
	stored := store.identities["oidc|subject"]
	store.mu.Unlock()
	require.Equal(t, successes[0].userID, stored.UserID)
}
