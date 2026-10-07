// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package unsub_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/notify/unsub"
)

type typedNilStore struct{ unsub.Store }

type stubStore struct{}

func (stubStore) Add(context.Context, string) error                  { return nil }
func (stubStore) IsSuppressed(context.Context, string) (bool, error) { return false, nil }
func (stubStore) Remove(context.Context, string) error               { return nil }

func TestNewTypedNilStoreReturnsError(t *testing.T) {
	t.Parallel()
	var store *typedNilStore
	if _, err := unsub.New(store, bytes.Repeat([]byte{0x42}, 32)); err == nil {
		t.Fatal("typed-nil store should be rejected")
	}
}

func TestNewRejectsWeakSecret(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 31} {
		_, err := unsub.New(stubStore{}, make([]byte, size))
		if err == nil {
			t.Fatalf("New accepted %d-byte secret", size)
		}
	}
	if _, err := unsub.New(stubStore{}, make([]byte, 32)); err != nil {
		t.Fatalf("New rejected 32-byte secret: %v", err)
	}
}

func TestNewClonesSecret(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{0x42}, 32)
	service, err := unsub.New(stubStore{}, secret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := service.URL("https://example.test/unsubscribe", "user@example.com")
	secret[0] ^= 0xff
	if got := service.URL("https://example.test/unsubscribe", "user@example.com"); got != want {
		t.Fatal("caller mutation changed outstanding unsubscribe signatures")
	}
}

func TestNewRejectsNilStore(t *testing.T) {
	t.Parallel()
	_, err := unsub.New(nil, bytes.Repeat([]byte{0x42}, 32))
	if err == nil || !errors.Is(err, unsub.ErrStoreRequired) {
		t.Fatalf("New: got %v", err)
	}
}
