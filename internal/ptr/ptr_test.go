// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ptr_test

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/internal/ptr"
)

func TestClone_copiesValue(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	got := ptr.Clone(&at)
	if got == &at {
		t.Fatal("Clone returned the source pointer")
	}
	if !got.Equal(at) {
		t.Fatalf("Clone = %v, want %v", got, at)
	}
}

func TestClone_isolatesMutation(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	got := ptr.Clone(&at)
	*got = got.Add(time.Hour)
	if !at.Equal(time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("source changed to %v after mutating the clone", at)
	}
}

func TestClone_nilStaysNil(t *testing.T) {
	t.Parallel()
	if got := ptr.Clone[time.Time](nil); got != nil {
		t.Fatalf("Clone(nil) = %v, want nil", got)
	}
}
