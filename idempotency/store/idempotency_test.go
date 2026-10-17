// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package store_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency/store"
)

func TestNewMemoryStoreWithClock_TypedNilFallsBack(t *testing.T) {
	t.Parallel()
	var clk *clockwork.FakeClock
	value := store.NewMemoryStoreWithClock(clk)
	ctx := context.Background()
	claim, err := value.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	err = value.Commit(
		ctx,
		"key",
		claim.Token,
		"fingerprint",
		store.CachedResponse{StatusCode: http.StatusCreated},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	completed, err := value.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil || completed.State != store.ClaimCompleted {
		t.Fatalf("Claim = (%+v, %v); want completed response", completed, err)
	}
}
