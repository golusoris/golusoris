package apikey_test

import (
	"context"
	"testing"
	"time"

	"github.com/golusoris/golusoris/auth/apikey"
)

// memStore is a minimal in-memory Store for tests.
type memStore struct {
	keys map[string]apikey.Key
}

func newMemStore() *memStore { return &memStore{keys: make(map[string]apikey.Key)} }

func (m *memStore) Save(_ context.Context, k apikey.Key) error {
	m.keys[k.ID] = k
	return nil
}

func (m *memStore) FindByID(_ context.Context, id string) (apikey.Key, error) {
	k, ok := m.keys[id]
	if !ok {
		return apikey.Key{}, &notFoundErr{}
	}
	return k, nil
}

func (m *memStore) Revoke(_ context.Context, id string) error {
	k, ok := m.keys[id]
	if !ok {
		return &notFoundErr{}
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

type notFoundErr struct{}

func (e *notFoundErr) Error() string { return "not found" }

func TestIssueAndVerify(t *testing.T) {
	t.Parallel()
	svc := apikey.New(newMemStore(), apikey.Options{
		Prefix:     "sk",
		HMACSecret: []byte("super-secret-hmac-key"),
	})

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
	svc := apikey.New(store, apikey.Options{HMACSecret: []byte("secret")})

	raw, key, _ := svc.Issue(context.Background(), "u", nil)
	_ = svc.Revoke(context.Background(), key.ID)

	if _, err := svc.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected error for revoked key")
	}
}

func TestVerifyTampered(t *testing.T) {
	t.Parallel()
	svc := apikey.New(newMemStore(), apikey.Options{HMACSecret: []byte("secret")})
	raw, _, _ := svc.Issue(context.Background(), "u", nil)

	// Flip last char.
	tampered := raw[:len(raw)-1] + "X"
	if _, err := svc.Verify(context.Background(), tampered); err == nil {
		t.Fatal("expected error for tampered key")
	}
}
