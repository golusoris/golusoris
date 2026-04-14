package htmx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/httpx/htmx"
)

func TestIsRequest(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if htmx.IsRequest(r) {
		t.Error("fresh request should not be HTMX")
	}
	r.Header.Set(htmx.HeaderRequest, "true")
	if !htmx.IsRequest(r) {
		t.Error("HX-Request: true should detect HTMX")
	}
}

func TestIsBoosted(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if htmx.IsBoosted(r) {
		t.Error("fresh request should not be boosted")
	}
	r.Header.Set(htmx.HeaderBoosted, "true")
	if !htmx.IsBoosted(r) {
		t.Error("HX-Boosted: true should be detected as boosted")
	}
}

func TestReplaceURL(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	htmx.ReplaceURL(w, "/current")
	if got := w.Header().Get(htmx.ResponseReplaceURL); got != "/current" {
		t.Errorf("ReplaceURL = %q, want /current", got)
	}
}

func TestResponseHelpers(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	htmx.PushURL(w, "/new")
	htmx.Redirect(w, "/login")
	htmx.Refresh(w)
	htmx.Reswap(w, "innerHTML")
	htmx.Retarget(w, "#main")
	htmx.Trigger(w, "dataChanged")

	cases := map[string]string{
		htmx.ResponsePushURL:  "/new",
		htmx.ResponseRedirect: "/login",
		htmx.ResponseRefresh:  "true",
		htmx.ResponseReswap:   "innerHTML",
		htmx.ResponseRetarget: "#main",
		htmx.ResponseTrigger:  "dataChanged",
	}
	for header, want := range cases {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
