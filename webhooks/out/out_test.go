package out_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/webhooks/out"
)

// memStore is a minimal in-memory Store for tests.
type memStore struct {
	endpoints  map[string]out.Endpoint
	deliveries map[string]out.Delivery
}

func newMemStore() *memStore {
	return &memStore{
		endpoints:  map[string]out.Endpoint{},
		deliveries: map[string]out.Delivery{},
	}
}

func (s *memStore) SaveEndpoint(_ context.Context, e out.Endpoint) error {
	s.endpoints[e.ID] = e
	return nil
}

func (s *memStore) FindEndpoint(_ context.Context, id string) (out.Endpoint, error) {
	e, ok := s.endpoints[id]
	if !ok {
		return out.Endpoint{}, errors.New("not found")
	}
	return e, nil
}

func (s *memStore) ListEndpoints(_ context.Context, event string) ([]out.Endpoint, error) {
	var out []out.Endpoint
	for _, e := range s.endpoints {
		if !e.Active {
			continue
		}
		if event == "" || len(e.Events) == 0 {
			out = append(out, e)
			continue
		}
		for _, ev := range e.Events {
			if ev == event {
				out = append(out, e)
				break
			}
		}
	}
	return out, nil
}

func (s *memStore) DeleteEndpoint(_ context.Context, id string) error {
	delete(s.endpoints, id)
	return nil
}

func (s *memStore) SaveDelivery(_ context.Context, d out.Delivery) error {
	s.deliveries[d.ID] = d
	return nil
}

func (s *memStore) FindDelivery(_ context.Context, id string) (out.Delivery, error) {
	d, ok := s.deliveries[id]
	if !ok {
		return out.Delivery{}, errors.New("not found")
	}
	return d, nil
}

func (s *memStore) ListDeadLetters(_ context.Context) ([]out.Delivery, error) {
	var dl []out.Delivery
	for _, d := range s.deliveries {
		if d.Status == out.StatusFailed {
			dl = append(dl, d)
		}
	}
	return dl, nil
}

func newDispatcher(t *testing.T, store *memStore) *out.Dispatcher {
	t.Helper()
	clk := clockwork.NewFakeClock()
	logger := nopLogger(t)
	return out.New(store, out.Options{
		MaxAttempts: 3,
		Timeout:     5 * time.Second,
		Backoff:     func(int) time.Duration { return 0 }, // no wait in tests
	}, logger, clk)
}

func TestDispatch_success(t *testing.T) {
	t.Parallel()
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	store := newMemStore()
	_ = store.SaveEndpoint(context.Background(), out.Endpoint{
		ID:     "ep1",
		URL:    srv.URL,
		Secret: "topsecret",
		Events: []string{"order.created"},
		Active: true,
	})

	d := newDispatcher(t, store)
	err := d.Dispatch(context.Background(), "order.created", map[string]string{"id": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Fatalf("expected 1 request, got %d", received.Load())
	}

	dls, _ := store.ListDeadLetters(context.Background())
	if len(dls) != 0 {
		t.Fatalf("expected no dead letters, got %d", len(dls))
	}
}

func TestDispatch_deadLetter(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	store := newMemStore()
	_ = store.SaveEndpoint(context.Background(), out.Endpoint{
		ID:     "ep1",
		URL:    srv.URL,
		Secret: "topsecret",
		Active: true,
	})

	d := newDispatcher(t, store)
	_ = d.Dispatch(context.Background(), "any", map[string]string{})

	dls, _ := store.ListDeadLetters(context.Background())
	if len(dls) != 1 {
		t.Fatalf("expected 1 dead letter, got %d", len(dls))
	}
	if dls[0].Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", dls[0].Attempts)
	}
}

func TestDispatch_skipsInactive(t *testing.T) {
	t.Parallel()
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	store := newMemStore()
	_ = store.SaveEndpoint(context.Background(), out.Endpoint{
		ID:     "ep1",
		URL:    srv.URL,
		Secret: "s",
		Active: false, // inactive
	})

	d := newDispatcher(t, store)
	_ = d.Dispatch(context.Background(), "any", nil)

	if received.Load() != 0 {
		t.Fatalf("inactive endpoint should not be called")
	}
}

func TestReplay(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n <= 3 { // first 3 fail → dead-letter during Dispatch
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	store := newMemStore()
	_ = store.SaveEndpoint(context.Background(), out.Endpoint{
		ID:     "ep1",
		URL:    srv.URL,
		Secret: "s",
		Active: true,
	})

	d := newDispatcher(t, store)
	// First dispatch — all attempts fail → dead letter.
	_ = d.Dispatch(context.Background(), "any", nil)

	dls, _ := store.ListDeadLetters(context.Background())
	if len(dls) != 1 {
		t.Fatalf("expected 1 dead letter before replay")
	}

	// Replay — 4th request, server succeeds.
	if err := d.Replay(context.Background(), dls[0].ID); err != nil {
		t.Fatalf("replay failed: %v", err)
	}
}

func nopLogger(_ *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
