// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/auth/session"
	gerr "github.com/golusoris/golusoris/core/errors"
)

func mustManager(t *testing.T, store session.Store, opts session.Options) *session.Manager {
	t.Helper()
	mgr, err := session.NewManager(store, opts)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

func TestLoadSaveRoundTrip(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()
	mgr := mustManager(t, store, session.Options{TTL: 0}) // 0 → default 24h

	// First request: no cookie → new session.
	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := mgr.Load(r1)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sess.Set("uid", "user-42")

	w := httptest.NewRecorder()
	if saveErr := mgr.SaveContext(r1.Context(), w, sess); saveErr != nil {
		t.Fatalf("Save: %v", saveErr)
	}

	// Second request: cookie set → load existing session.
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r2.AddCookie(c)
	}
	sess2, err := mgr.Load(r2)
	if err != nil {
		t.Fatalf("Load2: %v", err)
	}
	if v, ok := sess2.Get("uid").(string); !ok || v != "user-42" {
		t.Errorf("uid = %v, want user-42", sess2.Get("uid"))
	}
}

func TestDestroyExpiresCookie(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()
	mgr := mustManager(t, store, session.Options{})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, _ := mgr.Load(r)
	w := httptest.NewRecorder()
	_ = mgr.SaveContext(r.Context(), w, sess)

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r2.AddCookie(c)
	}
	w2 := httptest.NewRecorder()
	if err := mgr.Destroy(w2, r2); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// Cookie should be expired (MaxAge == -1).
	found := false
	for _, c := range w2.Result().Cookies() {
		if c.MaxAge == -1 {
			found = true
		}
	}
	if !found {
		t.Error("expected expired cookie after Destroy")
	}
}

func TestSessionCookiesAreSecureUnlessExplicitlyDisabled(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		allowInsecure bool
		wantSecure    bool
	}{
		{name: "secure default", wantSecure: true},
		{name: "explicit insecure development", allowInsecure: true, wantSecure: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mgr := mustManager(t, session.NewMemoryStore(), session.Options{
				AllowInsecureCookie: test.allowInsecure,
			})

			request := httptest.NewRequest(http.MethodPost, "/", nil)
			created, err := mgr.Load(request)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			saveResponse := httptest.NewRecorder()
			if err := mgr.SaveContext(request.Context(), saveResponse, created); err != nil {
				t.Fatalf("SaveContext: %v", err)
			}
			savedCookies := saveResponse.Result().Cookies()
			if len(savedCookies) != 1 {
				t.Fatalf("expected exactly one saved cookie, got %d", len(savedCookies))
			}
			if savedCookies[0].Secure != test.wantSecure || !savedCookies[0].HttpOnly {
				t.Errorf("saved cookie Secure=%v HttpOnly=%v", savedCookies[0].Secure, savedCookies[0].HttpOnly)
			}

			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.AddCookie(&http.Cookie{Name: "sid", Value: "some-session-id"})
			w := httptest.NewRecorder()
			if err := mgr.Destroy(w, r); err != nil {
				t.Fatalf("Destroy: %v", err)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("expected exactly one expiring cookie, got %d", len(cookies))
			}
			c := cookies[0]
			if c.MaxAge != -1 || c.Value != "" {
				t.Errorf("expected an expired empty cookie, got MaxAge=%d Value=%q", c.MaxAge, c.Value)
			}
			if c.Secure != test.wantSecure {
				t.Errorf("Secure = %v, want %v", c.Secure, test.wantSecure)
			}
			if !c.HttpOnly {
				t.Error("expected HttpOnly on the expiring cookie")
			}
		})
	}
}

// TestMemoryStoreSaveRejectsUnmarshalableValue covers the marshal error path
// added when MemoryStore.Save stopped discarding it. It is the only one of the
// store's three JSON error paths reachable through the public API: Save's
// unmarshal and Load's marshal can only fail on bytes json.Marshal itself just
// produced from a map[string]any, so they stay defensive.
func TestMemoryStoreSaveRejectsUnmarshalableValue(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()

	err := store.Save(t.Context(), "sid", map[string]any{"fn": func() {}}, time.Minute)
	if err == nil {
		t.Fatal("Save must reject a value json.Marshal cannot encode")
	}
	if !strings.Contains(err.Error(), "session/memory: marshal") {
		t.Errorf("error = %q, want it to name the marshal step", err)
	}
	if _, ok := errors.AsType[*json.UnsupportedTypeError](err); !ok {
		t.Errorf("error = %q, want the json cause to survive wrapping", err)
	}
	// A rejected Save must not leave a half-written entry behind.
	if _, loadErr := store.Load(t.Context(), "sid"); !isNotFoundErr(loadErr) {
		t.Errorf("Load after failed Save = %v, want not-found", loadErr)
	}
}

// TestMemoryStoreDeepCopies pins the reason Save and Load round-trip through
// JSON at all: neither the caller's map nor the returned map may alias what the
// store holds.
func TestMemoryStoreDeepCopies(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()

	data := map[string]any{"uid": "user-42", "nested": map[string]any{"k": "v"}}
	if err := store.Save(t.Context(), "sid", data, time.Minute); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mutating the caller's map after Save must not reach the store.
	data["uid"] = "tampered"
	data["nested"].(map[string]any)["k"] = "tampered"

	got, err := store.Load(t.Context(), "sid")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got["uid"] != "user-42" {
		t.Errorf("uid = %v, want user-42 (caller mutation leaked in)", got["uid"])
	}
	if nested, ok := got["nested"].(map[string]any); !ok || nested["k"] != "v" {
		t.Errorf("nested = %v, want map with k=v (caller mutation leaked in)", got["nested"])
	}

	// Mutating the loaded map must not reach the store either.
	got["uid"] = "tampered-again"
	again, err := store.Load(t.Context(), "sid")
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if again["uid"] != "user-42" {
		t.Errorf("uid = %v, want user-42 (loaded-map mutation leaked in)", again["uid"])
	}
}

// TestMemoryStoreLoadMissingAndExpired covers the two not-found paths: an id
// that was never saved (negative) and one whose TTL has just elapsed
// (boundary, driven by the injected clock).
func TestMemoryStoreLoadMissingAndExpired(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	store, err := session.NewMemoryStoreWithClock(clk)
	if err != nil {
		t.Fatalf("NewMemoryStoreWithClock: %v", err)
	}

	if _, err := store.Load(t.Context(), "never-saved"); !isNotFoundErr(err) {
		t.Errorf("Load(unknown) = %v, want not-found", err)
	}

	if err := store.Save(t.Context(), "sid", map[string]any{"k": "v"}, time.Minute); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Exactly at the expiry instant the entry is no longer live.
	clk.Advance(time.Minute)
	if _, err := store.Load(t.Context(), "sid"); !isNotFoundErr(err) {
		t.Errorf("Load at expiry = %v, want not-found", err)
	}
}

func TestMemoryStoreHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := store.Save(ctx, "sid", map[string]any{"k": "v"}, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("Save error = %v, want context.Canceled", err)
	}
	if _, err := store.Load(ctx, "sid"); !errors.Is(err, context.Canceled) {
		t.Errorf("Load error = %v, want context.Canceled", err)
	}
	if err := store.Delete(ctx, "sid"); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete error = %v, want context.Canceled", err)
	}
}

func TestDestroyPassesRequestContextToStore(t *testing.T) {
	t.Parallel()
	store := &contextStore{}
	mgr := mustManager(t, store, session.Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/logout", nil).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: "sid", Value: "session-id"})

	err := mgr.Destroy(httptest.NewRecorder(), r)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Destroy error = %v, want context.Canceled", err)
	}
}

func TestSaveContextPassesContextToStore(t *testing.T) {
	t.Parallel()
	mgr := mustManager(t, &contextStore{}, session.Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := mgr.SaveContext(ctx, httptest.NewRecorder(), &session.Session{ID: "session-id"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SaveContext error = %v, want context.Canceled", err)
	}
}

func TestMemoryStoreConcurrentAccess(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()
	const (
		workers    = 8
		iterations = 100
	)

	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := range workers {
		go func() {
			defer wg.Done()
			for iteration := range iterations {
				id := "sid-" + strconv.Itoa((worker+iteration)%4)
				if err := store.Save(t.Context(), id, map[string]any{"worker": worker}, time.Minute); err != nil {
					errCh <- err
					return
				}
				if _, err := store.Load(t.Context(), id); err != nil && !isNotFoundErr(err) {
					errCh <- err
					return
				}
				if iteration%3 == 0 {
					if err := store.Delete(t.Context(), id); err != nil {
						errCh <- err
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent store operation: %v", err)
	}
}

func TestNewManagerRejectsInvalidDependenciesAndTTL(t *testing.T) {
	t.Parallel()
	var typedNil *contextStore
	for _, test := range []struct {
		name  string
		store session.Store
		opts  session.Options
	}{
		{name: "nil store", store: nil},
		{name: "typed nil store", store: typedNil},
		{name: "negative TTL", store: session.NewMemoryStore(), opts: session.Options{TTL: -time.Nanosecond}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := session.NewManager(test.store, test.opts); err == nil {
				t.Fatal("NewManager accepted invalid configuration")
			}
		})
	}
	if _, err := session.NewManager(session.NewMemoryStore(), session.Options{}); err != nil {
		t.Fatalf("NewManager rejected valid defaults: %v", err)
	}
}

func TestNewMemoryStoreWithClockRejectsNil(t *testing.T) {
	t.Parallel()
	var typedNil *clockwork.FakeClock
	for _, clk := range []clockwork.Clock{nil, typedNil} {
		if _, err := session.NewMemoryStoreWithClock(clk); err == nil {
			t.Fatalf("NewMemoryStoreWithClock accepted %T", clk)
		}
	}
	if _, err := session.NewMemoryStoreWithClock(clockwork.NewFakeClock()); err != nil {
		t.Fatalf("NewMemoryStoreWithClock rejected valid clock: %v", err)
	}
}

type contextStore struct{}

func (contextStore) Load(ctx context.Context, _ string) (map[string]any, error) {
	return nil, ctx.Err()
}

func (contextStore) Save(ctx context.Context, _ string, _ map[string]any, _ time.Duration) error {
	return ctx.Err()
}

func (contextStore) Delete(ctx context.Context, _ string) error { return ctx.Err() }

// isNotFoundErr mirrors the package's own not-found check, which is unexported.
func isNotFoundErr(err error) bool {
	var e *gerr.Error
	return errors.As(err, &e) && e.Code == gerr.CodeNotFound
}
