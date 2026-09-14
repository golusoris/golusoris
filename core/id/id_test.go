// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package id_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/golusoris/golusoris/core/id"
)

func TestNewUUIDIsV7(t *testing.T) {
	t.Parallel()
	g := id.New()
	u, err := g.NewUUID()
	if err != nil {
		t.Fatalf("NewUUID: %v", err)
	}
	if u.Version() != 7 {
		t.Errorf("UUID version = %d, want 7", u.Version())
	}
}

func TestKSUIDIsUnique(t *testing.T) {
	t.Parallel()
	g := id.New()
	a, b := g.NewKSUID(), g.NewKSUID()
	if a == b {
		t.Error("two consecutive KSUIDs collided")
	}
}

// errReader always fails, standing in for a broken system random source.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// TestNewUUID_SurfacesRandomSourceFailure pins the HISS-07 contract: a failing
// random source yields a wrapped error and uuid.Nil instead of a panic.
//
//nolint:paralleltest // swaps the process-global uuid random source; must run serially.
func TestNewUUID_SurfacesRandomSourceFailure(t *testing.T) {
	randErr := errors.New("random source down")
	uuid.SetRand(errReader{err: randErr})
	t.Cleanup(func() { uuid.SetRand(nil) })

	u, err := id.New().NewUUID()
	if err == nil {
		t.Fatal("NewUUID with a failing random source returned a nil error")
	}
	if !errors.Is(err, randErr) {
		t.Errorf("NewUUID error = %v, want it to wrap the random-source error", err)
	}
	if u != uuid.Nil {
		t.Errorf("NewUUID on failure = %v, want uuid.Nil", u)
	}
}
