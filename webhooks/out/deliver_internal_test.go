// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package out

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

// fakeDeliverStore is a minimal Store fake for exercising deliver's
// extracted helpers directly, without going through Dispatch/Replay.
type fakeDeliverStore struct {
	saved []Delivery
}

func (s *fakeDeliverStore) SaveEndpoint(context.Context, Endpoint) error { return nil }

func (s *fakeDeliverStore) FindEndpoint(context.Context, string) (Endpoint, error) {
	return Endpoint{}, errors.New("not implemented")
}

func (s *fakeDeliverStore) ListEndpoints(context.Context, string) ([]Endpoint, error) {
	return nil, nil
}

func (s *fakeDeliverStore) DeleteEndpoint(context.Context, string) error { return nil }

func (s *fakeDeliverStore) SaveDelivery(_ context.Context, d Delivery) error {
	s.saved = append(s.saved, d)
	return nil
}

func (s *fakeDeliverStore) FindDelivery(context.Context, string) (Delivery, error) {
	return Delivery{}, errors.New("not implemented")
}

func (s *fakeDeliverStore) ListDeadLetters(context.Context) ([]Delivery, error) { return nil, nil }

func newDeliverTestDispatcher(store Store, clk clockwork.Clock, backoff func(int) time.Duration) *Dispatcher {
	opts := Options{Backoff: backoff}
	opts.defaults()
	return &Dispatcher{
		store:  store,
		opts:   opts,
		clk:    clk,
		logger: slog.New(slog.DiscardHandler),
	}
}

// Positive/boundary: the first attempt (attempts == 0) never waits, whatever
// the configured backoff.
func TestWaitBeforeRetry_FirstAttemptNoWait(t *testing.T) {
	t.Parallel()
	d := newDeliverTestDispatcher(&fakeDeliverStore{}, clockwork.NewFakeClock(), func(int) time.Duration {
		t.Fatal("backoff should not be consulted for the first attempt")
		return 0
	})
	if err := d.waitBeforeRetry(context.Background(), 0); err != nil {
		t.Fatalf("waitBeforeRetry(0) = %v, want nil", err)
	}
}

// Boundary: a zero/negative backoff duration skips the select/wait entirely.
func TestWaitBeforeRetry_ZeroBackoffSkipsWait(t *testing.T) {
	t.Parallel()
	d := newDeliverTestDispatcher(&fakeDeliverStore{}, clockwork.NewFakeClock(), func(int) time.Duration { return 0 })
	if err := d.waitBeforeRetry(context.Background(), 1); err != nil {
		t.Fatalf("waitBeforeRetry(1) with zero backoff = %v, want nil", err)
	}
}

// Negative: a cancelled context aborts the wait with ctx.Err() instead of
// blocking for the backoff duration.
func TestWaitBeforeRetry_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := newDeliverTestDispatcher(&fakeDeliverStore{}, clockwork.NewFakeClock(), func(int) time.Duration { return time.Minute })
	if err := d.waitBeforeRetry(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBeforeRetry with cancelled ctx = %v, want context.Canceled", err)
	}
}

// Positive: a successful attempt (nil error, 2xx) marks the delivery
// delivered, clears any prior error, persists it, and reports success.
func TestRecordAttempt_Success(t *testing.T) {
	t.Parallel()
	store := &fakeDeliverStore{}
	d := newDeliverTestDispatcher(store, clockwork.NewFakeClock(), nil)
	del := &Delivery{ID: "d1", Error: "prior failure"}

	ok := d.recordAttempt(context.Background(), del, 200, nil)

	if !ok {
		t.Fatal("recordAttempt(200, nil) = false, want true")
	}
	if del.Status != StatusDelivered || del.Error != "" || del.Attempts != 1 {
		t.Fatalf("del = %+v, want Status=delivered Error=\"\" Attempts=1", del)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 saved delivery, got %d", len(store.saved))
	}
}

// Negative: a transport error records the error text and reports failure.
func TestRecordAttempt_TransportError(t *testing.T) {
	t.Parallel()
	store := &fakeDeliverStore{}
	d := newDeliverTestDispatcher(store, clockwork.NewFakeClock(), nil)
	del := &Delivery{ID: "d1"}

	ok := d.recordAttempt(context.Background(), del, 0, errors.New("dial boom"))

	if ok {
		t.Fatal("recordAttempt with transport error = true, want false")
	}
	if del.Error != "dial boom" || del.Attempts != 1 {
		t.Fatalf("del = %+v, want Error=\"dial boom\" Attempts=1", del)
	}
}

// Boundary: a non-2xx status with a nil error records "HTTP <code>" rather
// than an empty error string.
func TestRecordAttempt_BadStatus(t *testing.T) {
	t.Parallel()
	store := &fakeDeliverStore{}
	d := newDeliverTestDispatcher(store, clockwork.NewFakeClock(), nil)
	del := &Delivery{ID: "d1"}

	ok := d.recordAttempt(context.Background(), del, 500, nil)

	if ok {
		t.Fatal("recordAttempt(500, nil) = true, want false")
	}
	if del.Error != "HTTP 500" {
		t.Fatalf("del.Error = %q, want %q", del.Error, "HTTP 500")
	}
}
