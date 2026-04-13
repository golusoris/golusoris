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
	if err := mgr.Save(w, sess); err != nil {
		t.Fatalf("Save: %v", err)
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
