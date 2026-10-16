// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/golusoris/golusoris/idempotency/store"
)

func TestMemoryStoreRejectsInvalidReservationArguments(t *testing.T) {
	t.Parallel()
	value := store.NewMemoryStore()
	ctx := context.Background()
	if _, err := value.Claim(ctx, "", "fingerprint", time.Minute); err == nil {
		t.Fatal("Claim accepted an empty key")
	}
	if _, err := value.Claim(ctx, "key", "fingerprint", -time.Second); err == nil {
		t.Fatal("Claim accepted a negative TTL")
	}
	if err := value.Commit(ctx, "key", "", "fingerprint", store.CachedResponse{}, time.Minute); err == nil {
		t.Fatal("Commit accepted an empty token")
	}
	if err := value.Release(ctx, "key", ""); err == nil {
		t.Fatal("Release accepted an empty token")
	}
}
