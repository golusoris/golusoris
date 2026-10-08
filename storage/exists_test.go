// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/golusoris/golusoris/storage"
)

func TestExistsFromStat(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	for _, tc := range []struct {
		name    string
		err     error
		want    bool
		wantErr error
	}{
		{name: "found", want: true},
		{name: "missing", err: storage.ErrNotFound},
		{name: "wrapped missing", err: fmt.Errorf("stat %q: %w", "k", storage.ErrNotFound)},
		{name: "backend failure", err: boom, wantErr: boom},
	} {
		got, err := storage.ExistsFromStat(storage.Object{Key: "k"}, tc.err)
		if got != tc.want || !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
			t.Errorf("%s: ExistsFromStat = %v, %v; want %v, %v", tc.name, got, err, tc.want, tc.wantErr)
		}
	}
}
