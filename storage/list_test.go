// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"errors"
	"testing"

	"github.com/golusoris/golusoris/storage"
)

func TestNormalizeListLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want int
		wantErr  bool
	}{
		{in: 0, want: storage.DefaultListLimit},
		{in: 1, want: 1},
		{in: storage.MaxListLimit, want: storage.MaxListLimit},
		{in: -1, wantErr: true},
		{in: storage.MaxListLimit + 1, wantErr: true},
	} {
		got, err := storage.NormalizeListLimit(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("NormalizeListLimit(%d) = %d, %v; want %d, err %v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestCleanListPrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want string
		wantErr  bool
	}{
		{in: "", want: ""},
		{in: "videos/", want: "videos/"},
		{in: "videos/2026", want: "videos/2026"},
		{in: "../escape", wantErr: true},
		{in: "a/./b", wantErr: true},
		{in: "a//", wantErr: true},
		{in: "/abs", wantErr: true},
	} {
		got, err := storage.CleanListPrefix(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("CleanListPrefix(%q) = %q, %v; want %q, err %v", tc.in, got, err, tc.want, tc.wantErr)
		}
		if tc.wantErr && !errors.Is(err, storage.ErrUnsafeKey) {
			t.Errorf("CleanListPrefix(%q) error = %v, want ErrUnsafeKey", tc.in, err)
		}
	}
}
