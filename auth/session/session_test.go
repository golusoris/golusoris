// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package session_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/auth/session"
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
