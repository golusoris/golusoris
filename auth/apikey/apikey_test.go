// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apikey_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/apikey"
	gerr "github.com/golusoris/golusoris/core/errors"
)

var testHMACSecret = strings.Repeat("x", 32)

// memStore is a minimal in-memory Store for tests.
type memStore struct {
	keys    map[string]apikey.Key
	findIDs []string
	findErr error
}

func newMemStore() *memStore { return &memStore{keys: make(map[string]apikey.Key)} }

func (m *memStore) Save(_ context.Context, k apikey.Key) error {
	m.keys[k.ID] = k
	return nil
}

func (m *memStore) FindByID(_ context.Context, id string) (apikey.Key, error) {
	m.findIDs = append(m.findIDs, id)
	if m.findErr != nil {
		return apikey.Key{}, m.findErr
	}
	k, ok := m.keys[id]
	if !ok {
		return apikey.Key{}, gerr.NotFound("api key not found")
	}
	return k, nil
}

func (m *memStore) Revoke(_ context.Context, id string) error {
	k, ok := m.keys[id]
	if !ok {
		return gerr.NotFound("api key not found")
	}
	now := time.Now()
	k.RevokedAt = &now
	m.keys[id] = k
	return nil
}

func (m *memStore) ListByOwner(_ context.Context, ownerID string) ([]apikey.Key, error) {
	var out []apikey.Key
	for _, k := range m.keys {
		if k.OwnerID == ownerID && k.RevokedAt == nil {
			out = append(out, k)
		}
	}
	return out, nil
}

func TestNew_EmptySecret(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 1, 31} {
		if _, err := apikey.New(newMemStore(), apikey.Options{HMACSecret: make([]byte, size)}); err == nil {
			t.Fatalf("expected error for %d-byte HMACSecret", size)
		}
	}
	_, err := apikey.New(newMemStore(), apikey.Options{HMACSecret: make([]byte, 32)})
	require.NoError(t, err)
}

func TestNew_RejectsNilStore(t *testing.T) {
	t.Parallel()
	_, err := apikey.New(nil, apikey.Options{HMACSecret: []byte(testHMACSecret)})
	require.Error(t, err)
}

func TestNew_HandlesTypedNilDependencies(t *testing.T) {
	t.Parallel()

	t.Run("store is rejected", func(t *testing.T) {
		t.Parallel()
		var store *memStore
		_, err := apikey.New(store, apikey.Options{HMACSecret: []byte(testHMACSecret)})
		require.Error(t, err)
	})

	t.Run("optional clock uses default", func(t *testing.T) {
		t.Parallel()
		var clk *clockwork.FakeClock
		svc, err := apikey.New(newMemStore(), apikey.Options{
			HMACSecret: []byte(testHMACSecret),
			Clock:      clk,
		})
		require.NoError(t, err)
		require.NotPanics(t, func() {
			_, _, err = svc.Issue(t.Context(), "owner", nil)
		})
		require.NoError(t, err)
	})
}

func TestIssueAndVerify(t *testing.T) {
	t.Parallel()
	svc, err := apikey.New(newMemStore(), apikey.Options{
		Prefix:     "sk",
		HMACSecret: []byte(testHMACSecret),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	raw, key, err := svc.Issue(context.Background(), "user-1", []string{"read"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if raw == "" || key.ID == "" {
		t.Fatal("expected non-empty raw and ID")
	}

	found, err := svc.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if found.OwnerID != "user-1" {
		t.Errorf("OwnerID = %q, want user-1", found.OwnerID)
	}
}

func TestVerifyRevoked(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	svc, err := apikey.New(store, apikey.Options{HMACSecret: []byte(testHMACSecret)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	raw, key, _ := svc.Issue(context.Background(), "u", nil)
	_ = svc.Revoke(context.Background(), key.ID)

	if _, err := svc.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected error for revoked key")
	}
}

func TestVerifyTampered(t *testing.T) {
	t.Parallel()
	svc, err := apikey.New(newMemStore(), apikey.Options{HMACSecret: []byte(testHMACSecret)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, _, _ := svc.Issue(context.Background(), "u", nil)

	// Flip the last char to a guaranteed-different one. Replacing it with a
	// fixed "X" is a no-op ~1/62 of the time (when the random last char is
	// already 'X'), which made this test flaky.
	repl := byte('X')
	if raw[len(raw)-1] == repl {
		repl = 'Y'
	}
	tampered := raw[:len(raw)-1] + string(repl)
	if _, err := svc.Verify(context.Background(), tampered); err == nil {
		t.Fatal("expected error for tampered key")
	}
}

func TestIssueKeepsIDsDistinctForUnderscorePrefix(t *testing.T) {
	t.Parallel()
	svc, err := apikey.New(newMemStore(), apikey.Options{
		Prefix:     "tenant_production",
		HMACSecret: []byte(testHMACSecret),
	})
	require.NoError(t, err)
	_, first, err := svc.Issue(context.Background(), "u", nil)
	require.NoError(t, err)
	_, second, err := svc.Issue(context.Background(), "u", nil)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.True(t, strings.HasPrefix(first.ID, "tenant_production_"))
}

func TestVerifyFindsPreUpgradePersistedID(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"sk", "tenant_production"} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			store := newMemStore()
			svc, err := apikey.New(store, apikey.Options{
				Prefix:     prefix,
				HMACSecret: []byte(testHMACSecret),
			})
			require.NoError(t, err)
			raw, key, err := svc.Issue(context.Background(), "owner", nil)
			require.NoError(t, err)

			parts := strings.SplitN(raw, "_", 2)
			require.Len(t, parts, 2)
			legacyID := parts[0] + "_" + parts[1][:8]
			delete(store.keys, key.ID)
			key.ID = legacyID
			store.keys[legacyID] = key
			store.findIDs = nil

			verified, err := svc.Verify(context.Background(), raw)
			require.NoError(t, err)
			require.Equal(t, legacyID, verified.ID)
			require.Len(t, store.findIDs, 2)
			require.Equal(t, legacyID, store.findIDs[1])
		})
	}
}

func TestVerifyRejectsLegacyKeyFromDifferentPrefix(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	issuer, err := apikey.New(store, apikey.Options{
		Prefix:     "other",
		HMACSecret: []byte(testHMACSecret),
	})
	require.NoError(t, err)
	raw, key, err := issuer.Issue(context.Background(), "owner", nil)
	require.NoError(t, err)

	parts := strings.SplitN(raw, "_", 2)
	legacyID := parts[0] + "_" + parts[1][:8]
	delete(store.keys, key.ID)
	key.ID = legacyID
	store.keys[legacyID] = key

	verifier, err := apikey.New(store, apikey.Options{
		Prefix:     "sk",
		HMACSecret: []byte(testHMACSecret),
	})
	require.NoError(t, err)
	_, err = verifier.Verify(context.Background(), raw)
	require.Error(t, err)
}

func TestVerifyDoesNotHideCurrentIDStoreFailure(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	store.findErr = gerr.Internal("database unavailable")
	svc, err := apikey.New(store, apikey.Options{HMACSecret: []byte(testHMACSecret)})
	require.NoError(t, err)

	_, err = svc.Verify(context.Background(), "key_raw")
	require.Error(t, err)
	require.Len(t, store.findIDs, 1)
}

func TestServiceClonesSecretAndMetadata(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	secret := []byte(testHMACSecret)
	scopes := []string{"read"}
	svc, err := apikey.New(store, apikey.Options{HMACSecret: secret})
	require.NoError(t, err)
	raw, issued, err := svc.Issue(context.Background(), "owner", scopes)
	require.NoError(t, err)
	secret[0] = 'x'
	scopes[0] = "admin"
	issued.Scopes[0] = "write"
	issued.Hash[0] ^= 0xff
	verified, err := svc.Verify(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, []string{"read"}, verified.Scopes)

	verified.Scopes[0] = "admin"
	again, err := svc.Verify(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, []string{"read"}, again.Scopes)
}

func TestVerifyDoesNotExposeStoredExpiryPointer(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	store := newMemStore()
	svc, err := apikey.New(store, apikey.Options{
		HMACSecret: []byte(testHMACSecret),
		Clock:      clk,
	})
	require.NoError(t, err)
	raw, key, err := svc.Issue(context.Background(), "owner", nil)
	require.NoError(t, err)
	expiresAt := clk.Now().Add(time.Minute)
	key.ExpiresAt = &expiresAt
	store.keys[key.ID] = key

	verified, err := svc.Verify(context.Background(), raw)
	require.NoError(t, err)
	*verified.ExpiresAt = clk.Now().Add(time.Hour)
	clk.Advance(2 * time.Minute)

	_, err = svc.Verify(context.Background(), raw)
	require.Error(t, err)
}

func TestServiceRejectsBlankOwnerAndExactExpiry(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	store := newMemStore()
	svc, err := apikey.New(store, apikey.Options{HMACSecret: []byte(testHMACSecret), Clock: clk})
	require.NoError(t, err)
	_, _, err = svc.Issue(context.Background(), " ", nil)
	require.Error(t, err)

	raw, key, err := svc.Issue(context.Background(), "owner", nil)
	require.NoError(t, err)
	expiresAt := clk.Now().Add(time.Minute)
	key.ExpiresAt = &expiresAt
	store.keys[key.ID] = key
	clk.Advance(time.Minute)
	_, err = svc.Verify(context.Background(), raw)
	require.Error(t, err)
}
