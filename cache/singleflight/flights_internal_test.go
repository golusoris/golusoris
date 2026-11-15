// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package singleflight

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestFlightsEmptyAfterCompletion(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := New[string, int]()
		fn := func(context.Context) (int, error) { return 42, nil }
		value, _, err := g.Do(t.Context(), "key", fn)
		if value != 42 || err != nil || len(g.flights) != 0 {
			t.Fatalf("Do = %d, %v, flights %d", value, err, len(g.flights))
		}
		value, _, err = g.DoCancelable(t.Context(), "key", fn)
		if value != 42 || err != nil || len(g.flights) != 0 {
			t.Fatalf("DoCancelable = %d, %v, flights %d", value, err, len(g.flights))
		}
		gate := make(chan struct{})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, _, callErr := g.DoCancelable(ctx, "key", func(context.Context) (int, error) {
				<-gate
				return 42, nil
			})
			done <- callErr
		}()
		synctest.Wait()
		cancel()
		if cancelErr := <-done; !errors.Is(cancelErr, context.Canceled) {
			t.Errorf("cancelled waiter: %v", cancelErr)
		}
		if len(g.flights) != 1 {
			t.Errorf("running flights = %d, want 1", len(g.flights))
		}
		close(gate)
		synctest.Wait()
		if len(g.flights) != 0 {
			t.Errorf("finished flights = %d, want 0", len(g.flights))
		}
	})
}
