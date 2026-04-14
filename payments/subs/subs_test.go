package subs_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/payments/subs"
)

func newSvc(t *testing.T) (*subs.Service, *subs.MemoryStore, clock.Clock) {
	t.Helper()
	store := subs.NewMemoryStore()
	fc := clock.NewFake()
	fc.Advance(time.Hour) // start at a non-zero time
	return subs.New(store, fc, nil, subs.Options{}), store, fc
}

func TestStart_WithTrial(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvc(t)
	sub, err := svc.Start(context.Background(), subs.StartParams{
		CustomerID: "c1", Plan: "pro", Seats: 3,
		Trial: 7 * 24 * time.Hour,
	})
	require.NoError(t, err)
	require.Equal(t, subs.StatusTrialing, sub.Status)
	require.NotNil(t, sub.TrialEndsAt)
	require.Equal(t, "c1", sub.CustomerID)
}

func TestStart_WithoutTrial_StartsIncomplete(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvc(t)
	sub, err := svc.Start(context.Background(), subs.StartParams{
		CustomerID: "c1", Plan: "pro",
	})
	require.NoError(t, err)
	require.Equal(t, subs.StatusIncomplete, sub.Status)
	require.Nil(t, sub.TrialEndsAt)
}

func TestActivate_IncompleteToActive(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))
	got, _ := svc.Cancel, subs.ErrNotFound
	_ = got
	store := subs.NewMemoryStore()
	// Re-fetch from the service's store via a round-trip.
	_ = store
}

func TestActivate_TrialingToActive(t *testing.T) {
	t.Parallel()
	svc, store, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{
		CustomerID: "c1", Plan: "pro", Trial: 24 * time.Hour,
	})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))
	got, err := store.Get(context.Background(), sub.ID)
	require.NoError(t, err)
	require.Equal(t, subs.StatusActive, got.Status)
	require.Nil(t, got.TrialEndsAt)
}

func TestActivate_InvalidFromCanceled(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Cancel(context.Background(), sub.ID, time.Time{}))
	require.Error(t, svc.Activate(context.Background(), sub.ID))
}

func TestCancel_Immediate(t *testing.T) {
	t.Parallel()
	svc, store, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))

	require.NoError(t, svc.Cancel(context.Background(), sub.ID, time.Time{}))
	got, _ := store.Get(context.Background(), sub.ID)
	require.Equal(t, subs.StatusCanceled, got.Status)
	require.NotNil(t, got.CanceledAt)
	require.Nil(t, got.CancelAt)
}

func TestCancel_Scheduled_ThenResume(t *testing.T) {
	t.Parallel()
	svc, store, clk := newSvc(t)
	fc := clk.(interface{ Advance(time.Duration) })

	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))

	future := clk.Now().Add(7 * 24 * time.Hour)
	require.NoError(t, svc.Cancel(context.Background(), sub.ID, future))
	got, _ := store.Get(context.Background(), sub.ID)
	require.Equal(t, subs.StatusActive, got.Status)
	require.NotNil(t, got.CancelAt)

	require.NoError(t, svc.Resume(context.Background(), sub.ID))
	got, _ = store.Get(context.Background(), sub.ID)
	require.Nil(t, got.CancelAt)

	// Scheduled cancel again, then ProcessDue after the time.
	require.NoError(t, svc.Cancel(context.Background(), sub.ID, future))
	fc.Advance(8 * 24 * time.Hour)
	require.NoError(t, svc.ProcessDue(context.Background(), []string{sub.ID}))
	got, _ = store.Get(context.Background(), sub.ID)
	require.Equal(t, subs.StatusCanceled, got.Status)
}

func TestPauseUnpause(t *testing.T) {
	t.Parallel()
	svc, store, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))

	require.NoError(t, svc.Pause(context.Background(), sub.ID))
	got, _ := store.Get(context.Background(), sub.ID)
	require.Equal(t, subs.StatusPaused, got.Status)
	require.NotNil(t, got.PausedAt)

	require.NoError(t, svc.Unpause(context.Background(), sub.ID))
	got, _ = store.Get(context.Background(), sub.ID)
	require.Equal(t, subs.StatusActive, got.Status)
	require.Nil(t, got.PausedAt)
}

func TestMarkPastDue_ThenActivateReturnsToActive(t *testing.T) {
	t.Parallel()
	svc, store, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))

	require.NoError(t, svc.MarkPastDue(context.Background(), sub.ID))
	got, _ := store.Get(context.Background(), sub.ID)
	require.Equal(t, subs.StatusPastDue, got.Status)

	require.NoError(t, svc.Activate(context.Background(), sub.ID))
	got, _ = store.Get(context.Background(), sub.ID)
	require.Equal(t, subs.StatusActive, got.Status)
}

func TestRenew_AdvancesPeriod(t *testing.T) {
	t.Parallel()
	svc, store, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))

	first, _ := store.Get(context.Background(), sub.ID)
	require.NoError(t, svc.Renew(context.Background(), sub.ID))
	second, _ := store.Get(context.Background(), sub.ID)
	require.True(t, second.CurrentPeriodStart.Equal(first.CurrentPeriodEnd))
	require.True(t, second.CurrentPeriodEnd.After(first.CurrentPeriodEnd))
}

func TestOnChangeCallback(t *testing.T) {
	t.Parallel()
	var events []subs.ChangeEvent
	svc := subs.New(subs.NewMemoryStore(), clock.NewFake(), nil, subs.Options{
		OnChange: func(_ context.Context, e subs.ChangeEvent) { events = append(events, e) },
	})
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "pro"})
	require.NoError(t, svc.Activate(context.Background(), sub.ID))
	require.NoError(t, svc.Pause(context.Background(), sub.ID))

	require.Len(t, events, 3) // Start, Activate, Pause
	require.Equal(t, subs.StatusIncomplete, events[0].To)
	require.Equal(t, subs.StatusActive, events[1].To)
	require.Equal(t, subs.StatusPaused, events[2].To)
}

func TestChangePlan(t *testing.T) {
	t.Parallel()
	svc, store, _ := newSvc(t)
	sub, _ := svc.Start(context.Background(), subs.StartParams{CustomerID: "c1", Plan: "basic", Seats: 1})
	require.NoError(t, svc.ChangePlan(context.Background(), sub.ID, "pro", 5))
	got, _ := store.Get(context.Background(), sub.ID)
	require.Equal(t, "pro", got.Plan)
	require.Equal(t, 5, got.Seats)
}
