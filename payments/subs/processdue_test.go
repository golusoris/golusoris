// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package subs_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/payments/subs"
)

var errUpsertBoom = errors.New("boom")

// failingUpsertStore wraps a MemoryStore and fails Upsert for one ID so a
// due-cancel sweep meets a bad row.
type failingUpsertStore struct {
	*subs.MemoryStore
	failID string
}

func (f *failingUpsertStore) Upsert(ctx context.Context, s *subs.Subscription) error {
	if s.ID == f.failID {
		return errUpsertBoom
	}
	return f.MemoryStore.Upsert(ctx, s)
}

// TestProcessDue_CancelFailureDoesNotStallSweep covers the cancelDue failure
// path: one row whose cancel fails is logged and skipped, the next due row is
// still canceled, and ProcessDue returns nil. The second row is due exactly at
// now (boundary of !CancelAt.After(now)).
func TestProcessDue_CancelFailureDoesNotStallSweep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := subs.NewMemoryStore()
	store := &failingUpsertStore{MemoryStore: mem}
	fc := clock.NewFake()
	fc.Advance(time.Hour)
	var logs bytes.Buffer
	svc := subs.New(store, fc, slog.New(slog.NewTextHandler(&logs, nil)), subs.Options{})

	a, err := svc.Start(ctx, subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, err)
	b, err := svc.Start(ctx, subs.StartParams{CustomerID: "c2", Plan: "pro"})
	require.NoError(t, err)
	at := fc.Now().Add(time.Minute)
	require.NoError(t, svc.Cancel(ctx, a.ID, at))
	require.NoError(t, svc.Cancel(ctx, b.ID, at))
	fc.Advance(time.Minute) // now == at: due, not yet past

	store.failID = a.ID
	require.NoError(t, svc.ProcessDue(ctx, []string{a.ID, b.ID}))

	gotA, err := mem.Get(ctx, a.ID)
	require.NoError(t, err)
	require.NotEqual(t, subs.StatusCanceled, gotA.Status, "failed cancel must not be persisted")
	require.NotNil(t, gotA.CancelAt, "scheduled cancel stays pending for the next sweep")

	gotB, err := mem.Get(ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, subs.StatusCanceled, gotB.Status, "sweep must continue past the failed row")

	require.Contains(t, logs.String(), "due cancel failed")
	require.Contains(t, logs.String(), "cancel_at reached")
	require.Contains(t, logs.String(), errUpsertBoom.Error())
}

// TestProcessDue_TrialEndCancelFailureIsLogged covers the second cancelDue
// call site: a trial that ended without activation whose cancel fails.
func TestProcessDue_TrialEndCancelFailureIsLogged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := subs.NewMemoryStore()
	store := &failingUpsertStore{MemoryStore: mem}
	fc := clock.NewFake()
	fc.Advance(time.Hour)
	var logs bytes.Buffer
	svc := subs.New(store, fc, slog.New(slog.NewTextHandler(&logs, nil)), subs.Options{})

	sub, err := svc.Start(ctx, subs.StartParams{CustomerID: "c1", Plan: "pro", Trial: time.Minute})
	require.NoError(t, err)
	require.Equal(t, subs.StatusTrialing, sub.Status)
	fc.Advance(2 * time.Minute)

	store.failID = sub.ID
	require.NoError(t, svc.ProcessDue(ctx, []string{sub.ID}))

	got, err := mem.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, subs.StatusTrialing, got.Status)
	require.Contains(t, logs.String(), "trial ended without activation")
}
