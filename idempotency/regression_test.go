// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency"
	"github.com/golusoris/golusoris/tenancy"
)

func TestMiddleware_ConcurrentRequestConflicts(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	handler := idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			entered <- struct{}{}
			<-release
			w.WriteHeader(http.StatusCreated)
		},
	))

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- sendIdempotentRequest(handler, "/payments", "same-key", "payload") }()
	<-entered
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { secondDone <- sendIdempotentRequest(handler, "/payments", "same-key", "payload") }()

	var second *httptest.ResponseRecorder
	select {
	case second = <-secondDone:
	case <-entered:
		close(release)
		<-firstDone
		<-secondDone
		t.Fatalf("concurrent request executed handler; calls = %d", calls.Load())
	case <-time.After(time.Second):
		close(release)
		t.Fatal("concurrent request did not return a conflict")
	}
	close(release)
	first := <-firstDone
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d; want %d", first.Code, http.StatusCreated)
	}
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d; want %d", second.Code, http.StatusConflict)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d; want 1", calls.Load())
	}
}

func TestMiddleware_InFlightPayloadMismatchIsUnprocessable(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	handler := idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			close(entered)
			<-release
			w.WriteHeader(http.StatusCreated)
		},
	))

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- sendIdempotentRequest(handler, "/payments", "same-key", "first") }()
	<-entered
	second := sendIdempotentRequest(handler, "/payments", "same-key", "second")
	close(release)
	first := <-firstDone

	if first.Code != http.StatusCreated || second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("statuses = (%d, %d); want (201, 422)", first.Code, second.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d; want 1", calls.Load())
	}
}

func TestMiddleware_ScopesKeyByOperation(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handler := idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_, _ = io.WriteString(w, r.URL.Path)
		},
	))
	first := sendIdempotentRequest(handler, "/payments", "same-key", "payload")
	second := sendIdempotentRequest(handler, "/refunds", "same-key", "payload")
	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d; want 2 for different operations", calls.Load())
	}
	if first.Body.String() != "/payments" || second.Body.String() != "/refunds" {
		t.Fatalf("bodies = %q, %q; want operation-specific responses", first.Body, second.Body)
	}
}

func TestMiddleware_RejectsKeyReuseWithDifferentPayload(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handler := idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusCreated)
		},
	))
	first := sendIdempotentRequest(handler, "/payments", "same-key", "first")
	second := sendIdempotentRequest(handler, "/payments", "same-key", "second")
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d; want %d", first.Code, http.StatusCreated)
	}
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second status = %d; want %d", second.Code, http.StatusUnprocessableEntity)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d; want 1", calls.Load())
	}
}

func TestMiddleware_ScopesKeyByTenant(t *testing.T) {
	t.Parallel()
	tenantStore := tenancy.NewMemoryStore()
	if err := tenantStore.Add(tenancy.Tenant{ID: "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	if err := tenantStore.Add(tenancy.Tenant{ID: "tenant-b"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	idempotent := idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			tenant, _ := tenancy.FromContext(r.Context())
			_, _ = io.WriteString(w, tenant.ID)
		},
	))
	handler := tenancy.Middleware(tenancy.HeaderExtractor("X-Tenant-ID"), tenantStore)(idempotent)

	first := sendTenantRequest(handler, "tenant-a")
	second := sendTenantRequest(handler, "tenant-b")
	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d; want 2 for different tenants", calls.Load())
	}
	if first.Body.String() != "tenant-a" || second.Body.String() != "tenant-b" {
		t.Fatalf("bodies = %q, %q; want tenant-specific responses", first.Body, second.Body)
	}
}

func TestMiddleware_FirstWriteHeaderWins(t *testing.T) {
	t.Parallel()
	handler := idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.WriteHeader(http.StatusTeapot)
		},
	))
	first := sendIdempotentRequest(handler, "/payments", "same-key", "payload")
	second := sendIdempotentRequest(handler, "/payments", "same-key", "payload")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("statuses = %d, %d; want first status %d", first.Code, second.Code, http.StatusCreated)
	}
}

func TestMiddleware_EnforcesRequestBodyLimit(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handler := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{MaxRequestBody: 4},
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	response := sendIdempotentRequest(handler, "/payments", "key", "12345")
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d; want 0", calls.Load())
	}
}

func TestMiddleware_ResponseOverflowIsDeliveredButNotCached(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handler := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{MaxResponseBody: 4},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "12345")
	}))
	first := sendIdempotentRequest(handler, "/payments", "key", "payload")
	second := sendIdempotentRequest(handler, "/payments", "key", "payload")
	if first.Body.String() != "12345" || second.Body.String() != "12345" {
		t.Fatalf("bodies = %q, %q; want untruncated responses", first.Body, second.Body)
	}
	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d; want 2 for uncached oversized responses", calls.Load())
	}
}

func TestMiddleware_CustomScopeSeparatesPrincipals(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handler := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{Scope: idempotency.NewScopeFunc(func(r *http.Request) (string, error) {
			return r.Header.Get("X-Principal"), nil
		})},
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, r.Header.Get("X-Principal"))
	}))
	send := func(principal string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader("payload"))
		req.Header.Set("Idempotency-Key", "key")
		req.Header.Set("X-Principal", principal)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	first := send("alice")
	second := send("bob")
	if calls.Load() != 2 || first.Body.String() != "alice" || second.Body.String() != "bob" {
		t.Fatalf("calls/bodies = %d, %q, %q; want principal-scoped responses", calls.Load(), first.Body, second.Body)
	}
}

func TestOptions_areComparable(t *testing.T) {
	t.Parallel()
	opts := idempotency.Options{Header: "Idempotency-Key"}
	set := map[idempotency.Options]struct{}{opts: {}}
	if _, ok := set[opts]; !ok {
		t.Fatal("Options map key missing")
	}
}

func TestNewScopeFunc_nilReturnsNil(t *testing.T) {
	t.Parallel()
	if scope := idempotency.NewScopeFunc(nil); scope != nil {
		t.Fatalf("scope = %p; want nil", scope)
	}
}

func TestMiddleware_snapshotsScopeCallback(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	scope := idempotency.ScopeFunc(func(*http.Request) (string, error) {
		return "alice", nil
	})
	handler := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{Scope: &scope},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	first := sendIdempotentRequest(handler, "/payments", "key", "payload")
	scope = func(*http.Request) (string, error) { return "bob", nil }
	second := sendIdempotentRequest(handler, "/payments", "key", "payload")
	if first.Code != http.StatusNoContent || second.Code != http.StatusNoContent {
		t.Fatalf("statuses = %d, %d; want %d", first.Code, second.Code, http.StatusNoContent)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d; want 1 with snapshotted scope", calls.Load())
	}
}

func TestMiddleware_PanicReleasesReservation(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handler := idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				panic("boom")
			}
			w.WriteHeader(http.StatusNoContent)
		},
	))
	func() {
		defer func() { _ = recover() }()
		_ = sendIdempotentRequest(handler, "/payments", "key", "payload")
	}()
	response := sendIdempotentRequest(handler, "/payments", "key", "payload")
	if response.Code != http.StatusNoContent || calls.Load() != 2 {
		t.Fatalf("retry = (%d, calls %d); want 204 after released reservation", response.Code, calls.Load())
	}
}

func TestMemoryStore_ExpiredEntryCanBeReplaced(t *testing.T) {
	t.Parallel()
	clock := clockwork.NewFakeClockAt(time.Unix(1_700_000_000, 0))
	store := idempotency.NewMemoryStoreWithClock(clock)
	ctx := context.Background()
	oldClaim, err := store.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim old: %v", err)
	}
	err = store.Commit(
		ctx,
		"key",
		oldClaim.Token,
		"fingerprint",
		idempotency.CachedResponse{Body: []byte("old")},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("Commit old: %v", err)
	}
	clock.Advance(2 * time.Minute)
	newClaim, err := store.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil || newClaim.State != idempotency.ClaimAcquired {
		t.Fatalf("Claim expired = (%+v, %v); want acquired", newClaim, err)
	}
	err = store.Commit(
		ctx,
		"key",
		newClaim.Token,
		"fingerprint",
		idempotency.CachedResponse{Body: []byte("new")},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("Commit replacement: %v", err)
	}
	completed, err := store.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil || completed.State != idempotency.ClaimCompleted || string(completed.Response.Body) != "new" {
		t.Fatalf("Claim replacement = (%+v, %v); want new completed response", completed, err)
	}
}

func TestMemoryStore_StaleTokenCannotOverwriteReplacement(t *testing.T) {
	t.Parallel()
	clock := clockwork.NewFakeClockAt(time.Unix(1_700_000_000, 0))
	store := idempotency.NewMemoryStoreWithClock(clock)
	ctx := context.Background()
	stale, err := store.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim stale: %v", err)
	}
	clock.Advance(2 * time.Minute)
	current, err := store.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim current: %v", err)
	}
	err = store.Commit(
		ctx,
		"key",
		stale.Token,
		"fingerprint",
		idempotency.CachedResponse{Body: []byte("stale")},
		time.Minute,
	)
	if !errors.Is(err, idempotency.ErrReservationLost) {
		t.Fatalf("stale Commit error = %v; want ErrReservationLost", err)
	}
	if err := store.Commit(
		ctx,
		"key",
		current.Token,
		"fingerprint",
		idempotency.CachedResponse{Body: []byte("current")},
		time.Minute,
	); err != nil {
		t.Fatalf("Commit current: %v", err)
	}
}

func sendIdempotentRequest(handler http.Handler, target, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func sendTenantRequest(handler http.Handler, tenantID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader("payload"))
	req.Header.Set("Idempotency-Key", "same-key")
	req.Header.Set("X-Tenant-Id", tenantID)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
