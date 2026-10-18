// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency/store"
)

func TestMemoryStore_ExpiredEntryCanBeReplaced(t *testing.T) {
	t.Parallel()
	clock := clockwork.NewFakeClockAt(time.Unix(1_700_000_000, 0))
	value := store.NewMemoryStoreWithClock(clock)
	ctx := context.Background()
	oldClaim, err := value.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim old: %v", err)
	}
	err = value.Commit(
		ctx,
		"key",
		oldClaim.Token,
		"fingerprint",
		store.CachedResponse{Body: []byte("old")},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("Commit old: %v", err)
	}
	clock.Advance(2 * time.Minute)
	newClaim, err := value.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil || newClaim.State != store.ClaimAcquired {
		t.Fatalf("Claim expired = (%+v, %v); want acquired", newClaim, err)
	}
	err = value.Commit(
		ctx,
		"key",
		newClaim.Token,
		"fingerprint",
		store.CachedResponse{Body: []byte("new")},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("Commit replacement: %v", err)
	}
	completed, err := value.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil || completed.State != store.ClaimCompleted || string(completed.Response.Body) != "new" {
		t.Fatalf("Claim replacement = (%+v, %v); want new completed response", completed, err)
	}
}

func TestMemoryStore_StaleTokenCannotOverwriteReplacement(t *testing.T) {
	t.Parallel()
	clock := clockwork.NewFakeClockAt(time.Unix(1_700_000_000, 0))
	value := store.NewMemoryStoreWithClock(clock)
	ctx := context.Background()
	stale, err := value.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim stale: %v", err)
	}
	clock.Advance(2 * time.Minute)
	current, err := value.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim current: %v", err)
	}
	err = value.Commit(
		ctx,
		"key",
		stale.Token,
		"fingerprint",
		store.CachedResponse{Body: []byte("stale")},
		time.Minute,
	)
	if !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("stale Commit error = %v; want ErrReservationLost", err)
	}
	if err := value.Commit(
		ctx,
		"key",
		current.Token,
		"fingerprint",
		store.CachedResponse{Body: []byte("current")},
		time.Minute,
	); err != nil {
		t.Fatalf("Commit current: %v", err)
	}
}
