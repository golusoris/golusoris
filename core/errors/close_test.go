// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package errors_test

import (
	stderrors "errors"
	"testing"

	"github.com/golusoris/golusoris/core/errors"
)

type closer struct{ err error }

func (c closer) Close() error { return c.err }

var errClose = stderrors.New("close failed")

func TestCloseInto(t *testing.T) {
	t.Parallel()
	primary := stderrors.New("primary")
	tests := []struct {
		name    string
		c       closer
		initial error
		wantIs  error
	}{
		{name: "close ok, no primary", c: closer{}, initial: nil, wantIs: nil},
		{name: "close fails, no primary → close error surfaces", c: closer{err: errClose}, initial: nil, wantIs: errClose},
		{name: "close fails, primary set → primary wins", c: closer{err: errClose}, initial: primary, wantIs: primary},
		{name: "close ok, primary set → untouched", c: closer{}, initial: primary, wantIs: primary},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.initial
			errors.CloseInto(tc.c, &err, "test: close")
			if tc.wantIs == nil && err != nil {
				t.Fatalf("got %v want nil", err)
			}
			if tc.wantIs != nil && !stderrors.Is(err, tc.wantIs) {
				t.Fatalf("got %v want %v", err, tc.wantIs)
			}
			if stderrors.Is(tc.wantIs, errClose) && err.Error() != "test: close: close failed" {
				t.Fatalf("op prefix missing: %q", err.Error())
			}
		})
	}
	// Boundary: nil closer / nil errp are no-ops, never panics.
	var err error
	errors.CloseInto(nil, &err, "x")
	errors.CloseInto(closer{err: errClose}, nil, "x")
	if err != nil {
		t.Fatal("nil closer must not set an error")
	}
}

func TestCloseJoin(t *testing.T) {
	t.Parallel()
	primary := stderrors.New("primary")
	err := primary
	errors.CloseJoin(closer{err: errClose}, &err, "test: close")
	if !stderrors.Is(err, primary) || !stderrors.Is(err, errClose) {
		t.Fatalf("both errors must be joined, got %v", err)
	}
	err = nil
	errors.CloseJoin(closer{}, &err, "test: close")
	if err != nil {
		t.Fatalf("clean close must leave nil, got %v", err)
	}
}
