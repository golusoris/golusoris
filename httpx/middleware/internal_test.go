// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEtagRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	e := &etagRecorder{ResponseWriter: httptest.NewRecorder()}
	e.WriteHeader(http.StatusCreated)
	if e.status != http.StatusCreated {
		t.Errorf("status = %d, want %d", e.status, http.StatusCreated)
	}
}

func TestStatusOrDefault_zero(t *testing.T) {
	t.Parallel()
	if got := statusOrDefault(0); got != http.StatusOK {
		t.Errorf("statusOrDefault(0) = %d, want %d", got, http.StatusOK)
	}
}

func TestStatusOrDefault_nonzero(t *testing.T) {
	t.Parallel()
	if got := statusOrDefault(404); got != 404 {
		t.Errorf("statusOrDefault(404) = %d, want 404", got)
	}
}

func TestStatusRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	s := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	s.WriteHeader(http.StatusCreated)
	if s.status != http.StatusCreated {
		t.Errorf("status = %d, want %d", s.status, http.StatusCreated)
	}
}

// TestFirstForwardedEntry_multipleEntries: the client entry (before the
// first comma) is trimmed and returned.
func TestFirstForwardedEntry_multipleEntries(t *testing.T) {
	t.Parallel()
	if got := firstForwardedEntry("203.0.113.5,  10.0.0.1"); got != "203.0.113.5" {
		t.Errorf("firstForwardedEntry = %q, want 203.0.113.5", got)
	}
}

// TestFirstForwardedEntry_singleEntry: no comma at all -> value unchanged.
func TestFirstForwardedEntry_singleEntry(t *testing.T) {
	t.Parallel()
	if got := firstForwardedEntry("203.0.113.5"); got != "203.0.113.5" {
		t.Errorf("firstForwardedEntry = %q, want 203.0.113.5", got)
	}
}

// TestFirstForwardedEntry_leadingComma is the boundary at idx==0: a comma as
// the very first byte does not satisfy idx>0, so the value passes through
// untrimmed — this is existing behavior, preserved on extraction.
func TestFirstForwardedEntry_leadingComma(t *testing.T) {
	t.Parallel()
	if got := firstForwardedEntry(",203.0.113.5"); got != ",203.0.113.5" {
		t.Errorf("firstForwardedEntry = %q, want unchanged", got)
	}
}

// TestRewriteRemoteAddrFromXFF_untrustedPeer: no nets configured -> no-op.
func TestRewriteRemoteAddrFromXFF_noTrustedNets(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	rewriteRemoteAddrFromXFF(r, nil)
	if r.RemoteAddr != "10.1.2.3:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged", r.RemoteAddr)
	}
}

// TestRewriteRemoteAddrFromXFF_noHeader: trusted peer but no
// X-Forwarded-For header present -> no-op.
func TestRewriteRemoteAddrFromXFF_noHeader(t *testing.T) {
	t.Parallel()
	nets := parseCIDRs([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	rewriteRemoteAddrFromXFF(r, nets)
	if r.RemoteAddr != "10.1.2.3:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged", r.RemoteAddr)
	}
}

// TestRewriteRemoteAddrFromXFF_emptyAfterTrim is the boundary where the
// derived entry trims to empty (all-whitespace before the comma) -> no-op.
func TestRewriteRemoteAddrFromXFF_emptyAfterTrim(t *testing.T) {
	t.Parallel()
	nets := parseCIDRs([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:1234"
	r.Header.Set("X-Forwarded-For", "   ,10.0.0.1")
	rewriteRemoteAddrFromXFF(r, nets)
	if r.RemoteAddr != "10.1.2.3:1234" {
		t.Errorf("RemoteAddr = %q, want unchanged", r.RemoteAddr)
	}
}
