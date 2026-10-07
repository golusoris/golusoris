// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency"
)

const (
	conformanceTTL = time.Minute
	fingerprintA   = "fingerprint-a"
	fingerprintB   = "fingerprint-b"
	// concurrentClaimers bounds the goroutines racing one key.
	concurrentClaimers = 8
)

// storeHarness is one isolated store under test; advance moves the store's
// notion of time forward by d.
type storeHarness struct {
	store   idempotency.Store
	advance func(t *testing.T, d time.Duration)
}

type conformanceCase struct {
	name string
	run  func(t *testing.T, h storeHarness)
}

// conformanceBase anchors every fake clock in the suite.
func conformanceBase() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// runStoreConformance runs the shared Store contract against newHarness;
// every case uses its own harness and keys, so cases run in parallel.
func runStoreConformance(t *testing.T, newHarness func(t *testing.T) storeHarness) {
	t.Helper()
	for _, tc := range conformanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func conformanceCases() []conformanceCase {
	return []conformanceCase{
		{"claim acquires a fresh key", caseClaimAcquires},
		{"in-flight retry reports in flight", caseInFlight},
		{"in-flight key reuse reports mismatch", caseInFlightMismatch},
		{"commit replays the stored response", caseCommitReplays},
		{"completed key reuse reports mismatch", caseCompletedMismatch},
		{"release frees the key", caseReleaseFrees},
		{"foreign token cannot commit or release", caseForeignToken},
		{"commit twice loses the reservation", caseCommitTwice},
		{"commit with another fingerprint is rejected", caseCommitMismatch},
		{"expiry boundary", caseExpiryBoundary},
		{"stale token cannot overwrite replacement", caseStaleToken},
		{"commit after expiry loses the reservation", caseCommitAfterExpiry},
		{"concurrent claims acquire once", caseConcurrentClaims},
		{"invalid arguments are rejected", caseInvalidArguments},
		{"canceled context is rejected", caseCanceledContext},
	}
}

func claim(t *testing.T, store idempotency.Store, key, fingerprint string) idempotency.ClaimResult {
	t.Helper()
	result, err := store.Claim(context.Background(), key, fingerprint, conformanceTTL)
	if err != nil {
		t.Fatalf("Claim(%q, %q): %v", key, fingerprint, err)
	}
	return result
}

func acquire(t *testing.T, store idempotency.Store, key string) string {
	t.Helper()
	result := claim(t, store, key, fingerprintA)
	if result.State != idempotency.ClaimAcquired || result.Token == "" {
		t.Fatalf("Claim(%q) = %+v; want acquired with token", key, result)
	}
	return result.Token
}

func commit(store idempotency.Store, key, token, fingerprint string, response idempotency.CachedResponse) error {
	return store.Commit(context.Background(), key, token, fingerprint, response, conformanceTTL)
}

func sampleResponse() idempotency.CachedResponse {
	return idempotency.CachedResponse{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Multi": {"one", "two"}},
		Body:       []byte{0x00, 0xff, '{', '}'},
	}
}

func requireState(t *testing.T, got idempotency.ClaimResult, want idempotency.ClaimState) {
	t.Helper()
	if got.State != want {
		t.Fatalf("claim state = %d; want %d (%+v)", got.State, want, got)
	}
	if want != idempotency.ClaimAcquired && got.Token != "" {
		t.Fatalf("claim token = %q; want empty for state %d", got.Token, want)
	}
}

func caseClaimAcquires(t *testing.T, h storeHarness) {
	t.Helper()
	first := acquire(t, h.store, t.Name())
	second := acquire(t, h.store, t.Name()+"/other")
	if first == second {
		t.Fatalf("tokens for distinct keys are equal: %q", first)
	}
}

func caseInFlight(t *testing.T, h storeHarness) {
	t.Helper()
	acquire(t, h.store, t.Name())
	requireState(t, claim(t, h.store, t.Name(), fingerprintA), idempotency.ClaimInFlight)
}

func caseInFlightMismatch(t *testing.T, h storeHarness) {
	t.Helper()
	acquire(t, h.store, t.Name())
	requireState(t, claim(t, h.store, t.Name(), fingerprintB), idempotency.ClaimFingerprintMismatch)
}

func caseCommitReplays(t *testing.T, h storeHarness) {
	t.Helper()
	for name, response := range map[string]idempotency.CachedResponse{
		"full":  sampleResponse(),
		"empty": {StatusCode: http.StatusNoContent},
	} {
		key := t.Name() + "/" + name
		token := acquire(t, h.store, key)
		if err := commit(h.store, key, token, fingerprintA, response); err != nil {
			t.Fatalf("Commit %s: %v", name, err)
		}
		got := claim(t, h.store, key, fingerprintA)
		requireState(t, got, idempotency.ClaimCompleted)
		if got.Fingerprint != fingerprintA {
			t.Fatalf("%s fingerprint = %q; want %q", name, got.Fingerprint, fingerprintA)
		}
		requireSameResponse(t, got.Response, response)
	}
}

func requireSameResponse(t *testing.T, got, want idempotency.CachedResponse) {
	t.Helper()
	if got.StatusCode != want.StatusCode || !bytes.Equal(got.Body, want.Body) || len(got.Header) != len(want.Header) {
		t.Fatalf("response = %+v; want %+v", got, want)
	}
	for name, values := range want.Header {
		if strings.Join(got.Header.Values(name), "|") != strings.Join(values, "|") {
			t.Fatalf("header %s = %q; want %q", name, got.Header.Values(name), values)
		}
	}
}

func caseCompletedMismatch(t *testing.T, h storeHarness) {
	t.Helper()
	token := acquire(t, h.store, t.Name())
	if err := commit(h.store, t.Name(), token, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	requireState(t, claim(t, h.store, t.Name(), fingerprintB), idempotency.ClaimFingerprintMismatch)
}

func caseReleaseFrees(t *testing.T, h storeHarness) {
	t.Helper()
	token := acquire(t, h.store, t.Name())
	if err := h.store.Release(context.Background(), t.Name(), token); err != nil {
		t.Fatalf("Release: %v", err)
	}
	next := acquire(t, h.store, t.Name())
	if next == token {
		t.Fatalf("reclaimed token %q equals released token", next)
	}
	if err := h.store.Release(context.Background(), t.Name(), token); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("Release released token = %v; want ErrReservationLost", err)
	}
}

func caseForeignToken(t *testing.T, h storeHarness) {
	t.Helper()
	acquire(t, h.store, t.Name())
	if err := commit(h.store, t.Name(), "foreign", fingerprintA, sampleResponse()); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("Commit foreign token = %v; want ErrReservationLost", err)
	}
	if err := h.store.Release(context.Background(), t.Name(), "foreign"); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("Release foreign token = %v; want ErrReservationLost", err)
	}
	if err := h.store.Release(context.Background(), t.Name()+"/absent", "foreign"); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("Release absent key = %v; want ErrReservationLost", err)
	}
	requireState(t, claim(t, h.store, t.Name(), fingerprintA), idempotency.ClaimInFlight)
}

func caseCommitTwice(t *testing.T, h storeHarness) {
	t.Helper()
	token := acquire(t, h.store, t.Name())
	if err := commit(h.store, t.Name(), token, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := commit(h.store, t.Name(), token, fingerprintA, sampleResponse()); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("second Commit = %v; want ErrReservationLost", err)
	}
	if err := h.store.Release(context.Background(), t.Name(), token); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("Release completed = %v; want ErrReservationLost", err)
	}
	requireState(t, claim(t, h.store, t.Name(), fingerprintA), idempotency.ClaimCompleted)
}

func caseCommitMismatch(t *testing.T, h storeHarness) {
	t.Helper()
	token := acquire(t, h.store, t.Name())
	if err := commit(h.store, t.Name(), token, fingerprintB, sampleResponse()); !errors.Is(err, idempotency.ErrFingerprintMismatch) {
		t.Fatalf("Commit other fingerprint = %v; want ErrFingerprintMismatch", err)
	}
	if err := h.store.Release(context.Background(), t.Name(), token); err != nil {
		t.Fatalf("Release after rejected commit: %v", err)
	}
}

func caseExpiryBoundary(t *testing.T, h storeHarness) {
	t.Helper()
	acquire(t, h.store, t.Name())
	h.advance(t, conformanceTTL-time.Second)
	requireState(t, claim(t, h.store, t.Name(), fingerprintB), idempotency.ClaimFingerprintMismatch)
	h.advance(t, time.Second)
	requireState(t, claim(t, h.store, t.Name(), fingerprintB), idempotency.ClaimAcquired)
}

func caseStaleToken(t *testing.T, h storeHarness) {
	t.Helper()
	stale := acquire(t, h.store, t.Name())
	h.advance(t, 2*conformanceTTL)
	current := acquire(t, h.store, t.Name())
	if err := commit(h.store, t.Name(), stale, fingerprintA, sampleResponse()); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("stale Commit = %v; want ErrReservationLost", err)
	}
	if err := h.store.Release(context.Background(), t.Name(), stale); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("stale Release = %v; want ErrReservationLost", err)
	}
	if err := commit(h.store, t.Name(), current, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("current Commit: %v", err)
	}
}

func caseCommitAfterExpiry(t *testing.T, h storeHarness) {
	t.Helper()
	token := acquire(t, h.store, t.Name())
	h.advance(t, conformanceTTL)
	if err := commit(h.store, t.Name(), token, fingerprintA, sampleResponse()); !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("expired Commit = %v; want ErrReservationLost", err)
	}
	requireState(t, claim(t, h.store, t.Name(), fingerprintA), idempotency.ClaimAcquired)
}

func caseConcurrentClaims(t *testing.T, h storeHarness) {
	t.Helper()
	start := make(chan struct{})
	var acquired, inFlight atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, concurrentClaimers)
	for range concurrentClaimers {
		wg.Go(func() {
			<-start
			result, err := h.store.Claim(context.Background(), t.Name(), fingerprintA, conformanceTTL)
			switch {
			case err != nil:
				errs <- err
			case result.State == idempotency.ClaimAcquired:
				acquired.Add(1)
			case result.State == idempotency.ClaimInFlight:
				inFlight.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Claim: %v", err)
	}
	if acquired.Load() != 1 || inFlight.Load() != concurrentClaimers-1 {
		t.Fatalf("acquired/in-flight = %d/%d; want 1/%d", acquired.Load(), inFlight.Load(), concurrentClaimers-1)
	}
}

func caseInvalidArguments(t *testing.T, h storeHarness) {
	t.Helper()
	ctx := context.Background()
	checks := map[string]error{}
	_, checks["claim empty key"] = h.store.Claim(ctx, "", fingerprintA, conformanceTTL)
	_, checks["claim empty fingerprint"] = h.store.Claim(ctx, t.Name(), "", conformanceTTL)
	_, checks["claim zero TTL"] = h.store.Claim(ctx, t.Name(), fingerprintA, 0)
	checks["commit empty token"] = h.store.Commit(ctx, t.Name(), "", fingerprintA, sampleResponse(), conformanceTTL)
	checks["commit zero TTL"] = h.store.Commit(ctx, t.Name(), "token", fingerprintA, sampleResponse(), 0)
	checks["release empty token"] = h.store.Release(ctx, t.Name(), "")
	for name, err := range checks {
		if err == nil || errors.Is(err, idempotency.ErrReservationLost) {
			t.Errorf("%s = %v; want validation error", name, err)
		}
	}
	acquire(t, h.store, t.Name())
}

func caseCanceledContext(t *testing.T, h storeHarness) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.store.Claim(ctx, t.Name(), fingerprintA, conformanceTTL); !errors.Is(err, context.Canceled) {
		t.Fatalf("Claim canceled = %v; want context.Canceled", err)
	}
	acquire(t, h.store, t.Name())
}

// runSweepConformance checks the bounded expired-key sweep. The store's clock
// must start at base so every row it writes predates other cases' rows.
func runSweepConformance(t *testing.T, h storeHarness, sweeper idempotency.Sweeper) {
	t.Helper()
	ctx := context.Background()
	prefix := t.Name() + "/sweep/"
	committed := acquire(t, h.store, prefix+"committed")
	if err := commit(h.store, prefix+"committed", committed, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	acquire(t, h.store, prefix+"a")
	acquire(t, h.store, prefix+"b")
	h.advance(t, 2*conformanceTTL)
	acquire(t, h.store, prefix+"live")
	for _, limit := range []int{0, idempotency.MaxSweepBatch + 1} {
		if _, err := sweeper.Sweep(ctx, limit); err == nil {
			t.Fatalf("Sweep(limit %d) = nil; want bound error", limit)
		}
	}
	for _, want := range []int64{2, 1, 0} {
		removed, err := sweeper.Sweep(ctx, 2)
		if err != nil || removed != want {
			t.Fatalf("Sweep(2) = (%d, %v); want (%d, nil)", removed, err, want)
		}
	}
	requireState(t, claim(t, h.store, prefix+"live", fingerprintA), idempotency.ClaimInFlight)
}

// requireSharedAcrossReplicas drives two middleware instances, one per
// replica store, and asserts one execution plus a cross-replica replay.
func requireSharedAcrossReplicas(t *testing.T, replicaA, replicaB idempotency.Store) {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	newHandler := func(store idempotency.Store) http.Handler {
		return idempotency.Middleware(store, idempotency.Options{})(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("paid"))
			},
		))
	}
	handlerA, handlerB := newHandler(replicaA), newHandler(replicaB)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- sendIdempotentRequest(handlerA, "/payments", "shared", "payload") }()
	<-entered
	concurrent := sendIdempotentRequest(handlerB, "/payments", "shared", "payload")
	close(release)
	first := <-firstDone
	replayed := sendIdempotentRequest(handlerB, "/payments", "shared", "payload")
	if first.Code != http.StatusCreated || concurrent.Code != http.StatusConflict {
		t.Fatalf("first/concurrent = %d/%d; want 201/409", first.Code, concurrent.Code)
	}
	if replayed.Code != http.StatusCreated || replayed.Body.String() != "paid" || calls.Load() != 1 {
		t.Fatalf("replay = (%d, %q, calls %d); want (201, paid, 1)", replayed.Code, replayed.Body, calls.Load())
	}
}

func newMemoryHarness(t *testing.T) storeHarness {
	t.Helper()
	clk := clockwork.NewFakeClockAt(conformanceBase())
	return storeHarness{
		store:   idempotency.NewMemoryStoreWithClock(clk),
		advance: func(_ *testing.T, d time.Duration) { clk.Advance(d) },
	}
}

func TestMemoryStore_Conformance(t *testing.T) {
	t.Parallel()
	runStoreConformance(t, newMemoryHarness)
}

func TestMemoryStore_Sweep(t *testing.T) {
	t.Parallel()
	h := newMemoryHarness(t)
	sweeper, ok := h.store.(idempotency.Sweeper)
	if !ok {
		t.Fatal("MemoryStore does not implement Sweeper")
	}
	runSweepConformance(t, h, sweeper)
}

// TestMemoryStore_NotSharedAcrossReplicas is the negative of the shared-store
// acceptance: two processes with their own MemoryStore both execute.
func TestMemoryStore_NotSharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	newHandler := func() http.Handler {
		return idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusCreated)
			},
		))
	}
	sendIdempotentRequest(newHandler(), "/payments", "shared", "payload")
	sendIdempotentRequest(newHandler(), "/payments", "shared", "payload")
	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d; want 2 with per-process stores", calls.Load())
	}
}
