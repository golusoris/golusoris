// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package storetest runs the shared store and middleware replay contracts.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/golusoris/golusoris/idempotency"
	"github.com/golusoris/golusoris/idempotency/store"
	"github.com/golusoris/golusoris/testutil/testctx"
)

const (
	conformanceTTL     = time.Minute
	conformanceTimeout = 30 * time.Second
	fingerprintA       = "fingerprint-a"
	fingerprintB       = "fingerprint-b"
	// concurrentClaimers bounds the goroutines racing one key.
	concurrentClaimers = 8
)

// Harness is one isolated store under test; advance moves the store's
// notion of time forward by d.
type Harness struct {
	Store   store.Store
	Advance func(t *testing.T, d time.Duration)
}

type conformanceCase struct {
	name string
	run  func(t *testing.T, h Harness)
}

// Base anchors every fake clock in the suite.
func Base() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// Run runs the shared Store contract against newHarness;
// every case uses its own harness and keys, so cases run in parallel.
func Run(t *testing.T, newHarness func(t *testing.T) Harness) {
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
		{"concurrent claims Acquire once", caseConcurrentClaims},
		{"invalid arguments are rejected", caseInvalidArguments},
		{"canceled context is rejected", caseCanceledContext},
	}
}

func claim(t *testing.T, value store.Store, key, fingerprint string) store.ClaimResult {
	t.Helper()
	result, err := value.Claim(testctx.Deadline(t, conformanceTimeout), key, fingerprint, conformanceTTL)
	if err != nil {
		t.Fatalf("Claim(%q, %q): %v", key, fingerprint, err)
	}
	return result
}

// Acquire reserves a fresh key and returns its ownership token.
func Acquire(t *testing.T, value store.Store, key string) string {
	t.Helper()
	result := claim(t, value, key, fingerprintA)
	if result.State != store.ClaimAcquired || result.Token == "" {
		t.Fatalf("Claim(%q) = %+v; want acquired with token", key, result)
	}
	return result.Token
}

func commit(ctx context.Context, value store.Store, key, token, fingerprint string, response store.CachedResponse) error {
	if err := value.Commit(ctx, key, token, fingerprint, response, conformanceTTL); err != nil {
		return fmt.Errorf("idempotency: test commit: %w", err)
	}
	return nil
}

func sampleResponse() store.CachedResponse {
	return store.CachedResponse{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Multi": {"one", "two"}},
		Body:       []byte{0x00, 0xff, '{', '}'},
	}
}

func requireState(t *testing.T, got store.ClaimResult, want store.ClaimState) {
	t.Helper()
	if got.State != want {
		t.Fatalf("claim state = %d; want %d (%+v)", got.State, want, got)
	}
	if want != store.ClaimAcquired && got.Token != "" {
		t.Fatalf("claim token = %q; want empty for state %d", got.Token, want)
	}
}

func caseClaimAcquires(t *testing.T, h Harness) {
	t.Helper()
	first := Acquire(t, h.Store, t.Name())
	second := Acquire(t, h.Store, t.Name()+"/other")
	if first == second {
		t.Fatalf("tokens for distinct keys are equal: %q", first)
	}
}

func caseInFlight(t *testing.T, h Harness) {
	t.Helper()
	Acquire(t, h.Store, t.Name())
	requireState(t, claim(t, h.Store, t.Name(), fingerprintA), store.ClaimInFlight)
}

func caseInFlightMismatch(t *testing.T, h Harness) {
	t.Helper()
	Acquire(t, h.Store, t.Name())
	requireState(t, claim(t, h.Store, t.Name(), fingerprintB), store.ClaimFingerprintMismatch)
}

func caseCommitReplays(t *testing.T, h Harness) {
	t.Helper()
	for name, response := range map[string]store.CachedResponse{
		"full":  sampleResponse(),
		"empty": {StatusCode: http.StatusNoContent},
	} {
		key := t.Name() + "/" + name
		token := Acquire(t, h.Store, key)
		if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, key, token, fingerprintA, response); err != nil {
			t.Fatalf("Commit %s: %v", name, err)
		}
		got := claim(t, h.Store, key, fingerprintA)
		requireState(t, got, store.ClaimCompleted)
		if got.Fingerprint != fingerprintA {
			t.Fatalf("%s fingerprint = %q; want %q", name, got.Fingerprint, fingerprintA)
		}
		requireSameResponse(t, got.Response, response)
	}
}

func requireSameResponse(t *testing.T, got, want store.CachedResponse) {
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

func caseCompletedMismatch(t *testing.T, h Harness) {
	t.Helper()
	token := Acquire(t, h.Store, t.Name())
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), token, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	requireState(t, claim(t, h.Store, t.Name(), fingerprintB), store.ClaimFingerprintMismatch)
}

func caseReleaseFrees(t *testing.T, h Harness) {
	t.Helper()
	token := Acquire(t, h.Store, t.Name())
	if err := h.Store.Release(testctx.Deadline(t, conformanceTimeout), t.Name(), token); err != nil {
		t.Fatalf("Release: %v", err)
	}
	next := Acquire(t, h.Store, t.Name())
	if next == token {
		t.Fatalf("reclaimed token %q equals released token", next)
	}
	if err := h.Store.Release(testctx.Deadline(t, conformanceTimeout), t.Name(), token); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("Release released token = %v; want ErrReservationLost", err)
	}
}

func caseForeignToken(t *testing.T, h Harness) {
	t.Helper()
	Acquire(t, h.Store, t.Name())
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), "foreign", fingerprintA, sampleResponse()); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("Commit foreign token = %v; want ErrReservationLost", err)
	}
	if err := h.Store.Release(testctx.Deadline(t, conformanceTimeout), t.Name(), "foreign"); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("Release foreign token = %v; want ErrReservationLost", err)
	}
	if err := h.Store.Release(testctx.Deadline(t, conformanceTimeout), t.Name()+"/absent", "foreign"); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("Release absent key = %v; want ErrReservationLost", err)
	}
	requireState(t, claim(t, h.Store, t.Name(), fingerprintA), store.ClaimInFlight)
}

func caseCommitTwice(t *testing.T, h Harness) {
	t.Helper()
	token := Acquire(t, h.Store, t.Name())
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), token, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), token, fingerprintA, sampleResponse()); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("second Commit = %v; want ErrReservationLost", err)
	}
	if err := h.Store.Release(testctx.Deadline(t, conformanceTimeout), t.Name(), token); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("Release completed = %v; want ErrReservationLost", err)
	}
	requireState(t, claim(t, h.Store, t.Name(), fingerprintA), store.ClaimCompleted)
}

func caseCommitMismatch(t *testing.T, h Harness) {
	t.Helper()
	token := Acquire(t, h.Store, t.Name())
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), token, fingerprintB, sampleResponse()); !errors.Is(err, store.ErrFingerprintMismatch) {
		t.Fatalf("Commit other fingerprint = %v; want ErrFingerprintMismatch", err)
	}
	if err := h.Store.Release(testctx.Deadline(t, conformanceTimeout), t.Name(), token); err != nil {
		t.Fatalf("Release after rejected commit: %v", err)
	}
}

func caseExpiryBoundary(t *testing.T, h Harness) {
	t.Helper()
	Acquire(t, h.Store, t.Name())
	h.Advance(t, conformanceTTL-time.Second)
	requireState(t, claim(t, h.Store, t.Name(), fingerprintB), store.ClaimFingerprintMismatch)
	h.Advance(t, time.Second)
	requireState(t, claim(t, h.Store, t.Name(), fingerprintB), store.ClaimAcquired)
}

func caseStaleToken(t *testing.T, h Harness) {
	t.Helper()
	stale := Acquire(t, h.Store, t.Name())
	h.Advance(t, 2*conformanceTTL)
	current := Acquire(t, h.Store, t.Name())
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), stale, fingerprintA, sampleResponse()); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("stale Commit = %v; want ErrReservationLost", err)
	}
	if err := h.Store.Release(testctx.Deadline(t, conformanceTimeout), t.Name(), stale); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("stale Release = %v; want ErrReservationLost", err)
	}
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), current, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("current Commit: %v", err)
	}
}

func caseCommitAfterExpiry(t *testing.T, h Harness) {
	t.Helper()
	token := Acquire(t, h.Store, t.Name())
	h.Advance(t, conformanceTTL)
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, t.Name(), token, fingerprintA, sampleResponse()); !errors.Is(err, store.ErrReservationLost) {
		t.Fatalf("expired Commit = %v; want ErrReservationLost", err)
	}
	requireState(t, claim(t, h.Store, t.Name(), fingerprintA), store.ClaimAcquired)
}

func caseConcurrentClaims(t *testing.T, h Harness) {
	t.Helper()
	start := make(chan struct{})
	var acquired, inFlight atomic.Int32
	var g errgroup.Group
	g.SetLimit(concurrentClaimers)
	for range concurrentClaimers {
		g.Go(func() error {
			<-start
			result, err := h.Store.Claim(testctx.Deadline(t, conformanceTimeout), t.Name(), fingerprintA, conformanceTTL)
			switch {
			case err != nil:
				return fmt.Errorf("claim: %w", err)
			case result.State == store.ClaimAcquired:
				acquired.Add(1)
			case result.State == store.ClaimInFlight:
				inFlight.Add(1)
			}
			return nil
		})
	}
	close(start)
	if err := g.Wait(); err != nil {
		t.Fatalf("concurrent Claim: %v", err)
	}
	if acquired.Load() != 1 || inFlight.Load() != concurrentClaimers-1 {
		t.Fatalf("acquired/in-flight = %d/%d; want 1/%d", acquired.Load(), inFlight.Load(), concurrentClaimers-1)
	}
}

func caseInvalidArguments(t *testing.T, h Harness) {
	t.Helper()
	ctx := testctx.Deadline(t, conformanceTimeout)
	checks := map[string]error{}
	_, checks["claim empty key"] = h.Store.Claim(ctx, "", fingerprintA, conformanceTTL)
	_, checks["claim empty fingerprint"] = h.Store.Claim(ctx, t.Name(), "", conformanceTTL)
	_, checks["claim zero TTL"] = h.Store.Claim(ctx, t.Name(), fingerprintA, 0)
	checks["commit empty token"] = h.Store.Commit(ctx, t.Name(), "", fingerprintA, sampleResponse(), conformanceTTL)
	checks["commit zero TTL"] = h.Store.Commit(ctx, t.Name(), "token", fingerprintA, sampleResponse(), 0)
	checks["release empty token"] = h.Store.Release(ctx, t.Name(), "")
	for name, err := range checks {
		if err == nil || errors.Is(err, store.ErrReservationLost) {
			t.Errorf("%s = %v; want validation error", name, err)
		}
	}
	Acquire(t, h.Store, t.Name())
}

func caseCanceledContext(t *testing.T, h Harness) {
	t.Helper()
	ctx, cancel := context.WithCancel(testctx.Deadline(t, conformanceTimeout))
	cancel()
	if _, err := h.Store.Claim(ctx, t.Name(), fingerprintA, conformanceTTL); !errors.Is(err, context.Canceled) {
		t.Fatalf("Claim canceled = %v; want context.Canceled", err)
	}
	Acquire(t, h.Store, t.Name())
}

// RunSweep checks the bounded expired-key sweep. The store's clock
// must start at base so every row it writes predates other cases' rows.
func RunSweep(t *testing.T, h Harness, sweeper store.Sweeper) {
	t.Helper()
	ctx := testctx.Deadline(t, conformanceTimeout)
	prefix := t.Name() + "/sweep/"
	committed := Acquire(t, h.Store, prefix+"committed")
	if err := commit(testctx.Deadline(t, conformanceTimeout), h.Store, prefix+"committed", committed, fingerprintA, sampleResponse()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	Acquire(t, h.Store, prefix+"a")
	Acquire(t, h.Store, prefix+"b")
	h.Advance(t, 2*conformanceTTL)
	Acquire(t, h.Store, prefix+"live")
	for _, limit := range []int{0, store.MaxSweepBatch + 1} {
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
	requireState(t, claim(t, h.Store, prefix+"live", fingerprintA), store.ClaimInFlight)
}

// SharedAcrossReplicas drives two middleware instances, one per
// replica store, and asserts one execution plus a cross-replica replay.
func SharedAcrossReplicas(t *testing.T, replicaA, replicaB store.Store) {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	newHandler := func(value store.Store) http.Handler {
		return idempotency.Middleware(value, idempotency.Options{})(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
				}
				w.WriteHeader(http.StatusCreated)
				if _, err := w.Write([]byte("paid")); err != nil {
					t.Errorf("write response: %v", err)
				}
			},
		))
	}
	handlerA, handlerB := newHandler(replicaA), newHandler(replicaB)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- sendIdempotentRequest(testctx.Deadline(t, conformanceTimeout), handlerA, "/payments", "shared", "payload")
	}()
	<-entered
	concurrent := sendIdempotentRequest(testctx.Deadline(t, conformanceTimeout), handlerB, "/payments", "shared", "payload")
	close(release)
	first := <-firstDone
	replayed := sendIdempotentRequest(testctx.Deadline(t, conformanceTimeout), handlerB, "/payments", "shared", "payload")
	if first.Code != http.StatusCreated || concurrent.Code != http.StatusConflict {
		t.Fatalf("first/concurrent = %d/%d; want 201/409", first.Code, concurrent.Code)
	}
	if replayed.Code != http.StatusCreated || replayed.Body.String() != "paid" || calls.Load() != 1 {
		t.Fatalf("replay = (%d, %q, calls %d); want (201, paid, 1)", replayed.Code, replayed.Body, calls.Load())
	}
}

func sendIdempotentRequest(ctx context.Context, handler http.Handler, target, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
