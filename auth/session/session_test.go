// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package session_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/auth/session"
	gerr "github.com/golusoris/golusoris/core/errors"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()
	mgr := session.NewManager(store, session.Options{TTL: 0}) // 0 → default 24h

	// First request: no cookie → new session.
	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := mgr.Load(r1)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sess.Set("uid", "user-42")

	w := httptest.NewRecorder()
	if saveErr := mgr.Save(w, sess); saveErr != nil {
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
	mgr := session.NewManager(store, session.Options{})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, _ := mgr.Load(r)
	w := httptest.NewRecorder()
	_ = mgr.Save(w, sess)

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

// TestDestroyCookieMirrorsSecureOption pins the justification of the
// cookie-missing-secure suppression in Destroy: the expiring cookie carries
// exactly Options.Secure (true in production) and is always HttpOnly.
func TestDestroyCookieMirrorsSecureOption(t *testing.T) {
	t.Parallel()
	for _, secure := range []bool{true, false} {
		t.Run(map[bool]string{true: "secure", false: "insecure"}[secure], func(t *testing.T) {
			t.Parallel()
			mgr := session.NewManager(session.NewMemoryStore(), session.Options{Secure: secure})

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
			if c.Secure != secure {
				t.Errorf("Secure = %v, want Options.Secure = %v", c.Secure, secure)
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
	var ute *json.UnsupportedTypeError
	if !errors.As(err, &ute) {
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
	store := session.NewMemoryStoreWithClock(clk)

	if _, err := store.Load(t.Context(), "never-saved"); !isNotFoundErr(err) {
		t.Errorf("Load(unknown) = %v, want not-found", err)
	}

	if err := store.Save(t.Context(), "sid", map[string]any{"k": "v"}, time.Minute); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Exactly at the expiry instant the entry is still live: the store
	// compares with After, not !Before.
	clk.Advance(time.Minute)
	if _, err := store.Load(t.Context(), "sid"); err != nil {
		t.Errorf("Load at the expiry instant = %v, want the entry to still be live", err)
	}

	clk.Advance(time.Nanosecond)
	if _, err := store.Load(t.Context(), "sid"); !isNotFoundErr(err) {
		t.Errorf("Load past expiry = %v, want not-found", err)
	}
}

// isNotFoundErr mirrors the package's own not-found check, which is unexported.
func isNotFoundErr(err error) bool {
	var e *gerr.Error
	return errors.As(err, &e) && e.Code == gerr.CodeNotFound
}
