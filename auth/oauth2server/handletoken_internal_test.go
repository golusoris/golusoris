// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

// postFormRequest builds a POST /token request whose r.PostForm is
// pre-populated, as it would be after http.Request.ParseForm.
func postFormRequest(t *testing.T, form url.Values) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/token", nil)
	r.PostForm = form
	return r
}

func TestResolveCode_Success(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	codes := NewMemoryCodeStore()
	req := AuthRequest{
		ClientID:    "c1",
		RedirectURI: "http://x/cb",
		ExpiresAt:   clk.Now().Add(time.Minute),
	}
	if err := codes.Save(context.Background(), Code{Value: "code-1", Req: req}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{opts: Options{Codes: codes, Clock: clk}}

	r := postFormRequest(t, url.Values{
		"code":         {"code-1"},
		"redirect_uri": {"http://x/cb"},
		"client_id":    {"c1"},
	})

	code, terr := s.resolveCode(r)
	if terr != nil {
		t.Fatalf("expected success, got tokenErrInfo %+v", terr)
	}
	if code.Value != "code-1" {
		t.Errorf("expected code-1, got %q", code.Value)
	}
}

func TestResolveCode_NotFound(t *testing.T) {
	t.Parallel()

	s := &Server{opts: Options{Codes: NewMemoryCodeStore(), Clock: clockwork.NewFakeClock()}}
	r := postFormRequest(t, url.Values{"code": {"missing"}})

	_, terr := s.resolveCode(r)
	if terr == nil {
		t.Fatal("expected error for unknown code")
	}
	if terr.code != "invalid_grant" || terr.desc != "code not found" {
		t.Errorf("unexpected tokenErrInfo: %+v", terr)
	}
}

func TestResolveCode_Expired(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	codes := NewMemoryCodeStore()
	if err := codes.Save(context.Background(), Code{
		Value: "code-1",
		Req:   AuthRequest{ExpiresAt: clk.Now().Add(-time.Second)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{opts: Options{Codes: codes, Clock: clk}}
	r := postFormRequest(t, url.Values{"code": {"code-1"}})

	_, terr := s.resolveCode(r)
	if terr == nil || terr.desc != "code expired" {
		t.Errorf("expected 'code expired', got %+v", terr)
	}
}

func TestResolveCode_RedirectMismatch(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	codes := NewMemoryCodeStore()
	if err := codes.Save(context.Background(), Code{
		Value: "code-1",
		Req:   AuthRequest{RedirectURI: "http://x/cb", ExpiresAt: clk.Now().Add(time.Minute)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{opts: Options{Codes: codes, Clock: clk}}
	r := postFormRequest(t, url.Values{"code": {"code-1"}, "redirect_uri": {"http://other/cb"}})

	_, terr := s.resolveCode(r)
	if terr == nil || terr.desc != "redirect_uri mismatch" {
		t.Errorf("expected 'redirect_uri mismatch', got %+v", terr)
	}
}

func TestResolveCode_ClientMismatch(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	codes := NewMemoryCodeStore()
	if err := codes.Save(context.Background(), Code{
		Value: "code-1",
		Req:   AuthRequest{ClientID: "c1", RedirectURI: "http://x/cb", ExpiresAt: clk.Now().Add(time.Minute)},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{opts: Options{Codes: codes, Clock: clk}}
	r := postFormRequest(t, url.Values{
		"code":         {"code-1"},
		"redirect_uri": {"http://x/cb"},
		"client_id":    {"someone-else"},
	})

	_, terr := s.resolveCode(r)
	if terr == nil || terr.desc != "client mismatch" {
		t.Errorf("expected 'client mismatch', got %+v", terr)
	}
}

func TestAuthenticateClient_PublicClientNeedsNoSecret(t *testing.T) {
	t.Parallel()

	clients := NewMemoryClientStore()
	clients.Add(Client{ID: "spa", PublicClient: true})
	s := &Server{opts: Options{Clients: clients}}
	r := postFormRequest(t, url.Values{"client_id": {"spa"}})

	client, terr := s.authenticateClient(r)
	if terr != nil {
		t.Fatalf("expected success, got %+v", terr)
	}
	if client.ID != "spa" {
		t.Errorf("expected spa, got %q", client.ID)
	}
}

func TestAuthenticateClient_UnknownClient(t *testing.T) {
	t.Parallel()

	s := &Server{opts: Options{Clients: NewMemoryClientStore()}}
	r := postFormRequest(t, url.Values{"client_id": {"ghost"}})

	_, terr := s.authenticateClient(r)
	if terr == nil || terr.desc != "unknown client" {
		t.Errorf("expected 'unknown client', got %+v", terr)
	}
}

func TestAuthenticateClient_ConfidentialGoodSecret(t *testing.T) {
	t.Parallel()

	clients := NewMemoryClientStore()
	clients.Add(Client{ID: "backend", Secret: "s3cr3t"})
	s := &Server{opts: Options{Clients: clients}}
	r := postFormRequest(t, url.Values{"client_id": {"backend"}, "client_secret": {"s3cr3t"}})

	client, terr := s.authenticateClient(r)
	if terr != nil {
		t.Fatalf("expected success, got %+v", terr)
	}
	if client.ID != "backend" {
		t.Errorf("expected backend, got %q", client.ID)
	}
}

func TestAuthenticateClient_ConfidentialBadSecret(t *testing.T) {
	t.Parallel()

	clients := NewMemoryClientStore()
	clients.Add(Client{ID: "backend", Secret: "s3cr3t"})
	s := &Server{opts: Options{Clients: clients}}
	r := postFormRequest(t, url.Values{"client_id": {"backend"}, "client_secret": {"wrong"}})

	_, terr := s.authenticateClient(r)
	if terr == nil || terr.desc != "bad secret" {
		t.Errorf("expected 'bad secret', got %+v", terr)
	}
}
