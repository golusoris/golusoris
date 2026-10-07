// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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

func TestResolveCode_ExpiresAtBoundary(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	codes := NewMemoryCodeStore()
	if err := codes.Save(context.Background(), Code{
		Value: "code-boundary",
		Req:   AuthRequest{ExpiresAt: clk.Now()},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s := &Server{opts: Options{Codes: codes, Clock: clk}}
	r := postFormRequest(t, url.Values{"code": {"code-boundary"}})
	if _, terr := s.resolveCode(r); terr == nil || terr.desc != "code expired" {
		t.Fatalf("resolveCode() error = %+v, want exact-boundary expiry", terr)
	}
}

func TestAuthorizePKCERequiresS256AndRFC7636Syntax(t *testing.T) {
	t.Parallel()
	verifier := "pkce-verifier-with-forty-three-or-more-characters"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	tests := []struct {
		name  string
		query url.Values
		valid bool
	}{
		{name: "valid S256", query: url.Values{"code_challenge": {challenge}, "code_challenge_method": {"S256"}}, valid: true},
		{name: "missing method", query: url.Values{"code_challenge": {challenge}}},
		{name: "plain rejected", query: url.Values{"code_challenge": {verifier}, "code_challenge_method": {"plain"}}},
		{name: "short challenge", query: url.Values{"code_challenge": {"short"}, "code_challenge_method": {"S256"}}},
		{name: "invalid character", query: url.Values{"code_challenge": {challenge[:42] + "+"}, "code_challenge_method": {"S256"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotChallenge, gotMethod, message := authorizePKCE(test.query)
			if test.valid {
				if message != "" || gotChallenge != challenge || gotMethod != "S256" {
					t.Fatalf("authorizePKCE() = %q, %q, %q", gotChallenge, gotMethod, message)
				}
				if !verifyPKCE(gotChallenge, gotMethod, verifier) {
					t.Fatal("verifyPKCE() rejected valid verifier")
				}
				return
			}
			if message == "" {
				t.Fatal("authorizePKCE() accepted invalid input")
			}
		})
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
	if err := clients.Register(Client{ID: "spa", RedirectURIs: []string{"https://spa.example/cb"}, PublicClient: true}); err != nil {
		t.Fatal(err)
	}
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
	if err := clients.Register(Client{ID: "backend", RedirectURIs: []string{"https://backend.example/cb"}, Secret: "s3cr3t"}); err != nil {
		t.Fatal(err)
	}
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
	if err := clients.Register(Client{ID: "backend", RedirectURIs: []string{"https://backend.example/cb"}, Secret: "s3cr3t"}); err != nil {
		t.Fatal(err)
	}
	s := &Server{opts: Options{Clients: clients}}
	r := postFormRequest(t, url.Values{"client_id": {"backend"}, "client_secret": {"wrong"}})

	_, terr := s.authenticateClient(r)
	if terr == nil || terr.desc != "bad secret" {
		t.Errorf("expected 'bad secret', got %+v", terr)
	}
}

func TestAuthenticateClientRejectsInvalidReturnedClient(t *testing.T) {
	t.Parallel()
	s := &Server{opts: Options{Clients: clientStoreFunc(func(context.Context, string) (Client, error) {
		return Client{
			ID:           "backend",
			RedirectURIs: []string{"https://backend.example/cb"},
		}, nil
	})}}
	r := postFormRequest(t, url.Values{"client_id": {"backend"}})
	_, terr := s.authenticateClient(r)
	if terr == nil || terr.code != "invalid_client" {
		t.Fatalf("authenticateClient() error = %+v, want invalid_client", terr)
	}
}

type clientStoreFunc func(context.Context, string) (Client, error)

func (f clientStoreFunc) Get(ctx context.Context, id string) (Client, error) {
	return f(ctx, id)
}

func TestMemoryClientStoreValidatesAndClonesPolicy(t *testing.T) {
	t.Parallel()
	store := NewMemoryClientStore()
	invalid := []Client{
		{PublicClient: true, RedirectURIs: []string{"https://app.example/cb"}},
		{ID: "client", PublicClient: true},
		{ID: "client", PublicClient: true, RedirectURIs: []string{"not a URI"}},
		{ID: "client", PublicClient: true, RedirectURIs: []string{"https://app.example/cb"}, Scopes: []string{""}},
		{ID: "client", RedirectURIs: []string{"https://app.example/cb"}},
	}
	for _, client := range invalid {
		if err := store.Register(client); err == nil {
			t.Fatalf("Add(%+v) accepted invalid client", client)
		}
	}

	redirects := []string{"https://app.example/cb"}
	scopes := []string{"read"}
	if err := store.Register(Client{ID: "client", PublicClient: true, RedirectURIs: redirects, Scopes: scopes}); err != nil {
		t.Fatal(err)
	}
	redirects[0] = "https://attacker.example/cb"
	scopes[0] = "admin"
	stored, err := store.Get(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	if stored.RedirectURIs[0] != "https://app.example/cb" || stored.Scopes[0] != "read" {
		t.Fatalf("stored client changed through caller alias: %+v", stored)
	}
	stored.RedirectURIs[0] = "https://mutated.example/cb"
	stored.Scopes[0] = "write"
	again, err := store.Get(t.Context(), "client")
	if err != nil {
		t.Fatal(err)
	}
	if again.RedirectURIs[0] != "https://app.example/cb" || again.Scopes[0] != "read" {
		t.Fatalf("stored client changed through returned alias: %+v", again)
	}
}

func TestMemoryClientStoreNativeRedirectPolicy(t *testing.T) {
	t.Parallel()
	valid := []string{
		"com.example.app:/oauth2redirect/example-provider",
		"https://app.example.com/oauth2redirect/example-provider",
		"http://127.0.0.1:51004/oauth2redirect/example-provider",
		"http://[::1]:61023/oauth2redirect/example-provider",
	}
	for _, redirect := range valid {
		store := NewMemoryClientStore()
		if err := store.Register(Client{ID: "native", PublicClient: true, RedirectURIs: []string{redirect}}); err != nil {
			t.Errorf("public native redirect %q rejected: %v", redirect, err)
		}
	}

	invalid := []Client{
		{ID: "native", PublicClient: true, RedirectURIs: []string{"myapp:/oauth2redirect"}},
		{ID: "native", PublicClient: true, RedirectURIs: []string{"com.example.app://oauth2redirect"}},
		{ID: "confidential", Secret: "secret", RedirectURIs: []string{"com.example.app:/oauth2redirect"}},
		{ID: "public-http", PublicClient: true, RedirectURIs: []string{"http://app.example.com/oauth2redirect"}},
		{ID: "confidential-http", Secret: "secret", RedirectURIs: []string{"http://app.example.com/oauth2redirect"}},
	}
	for _, client := range invalid {
		if err := NewMemoryClientStore().Register(client); err == nil {
			t.Errorf("invalid native client accepted: %+v", client)
		}
	}
}

func TestPickRegisteredAllowsOnlyPublicLoopbackPortVariance(t *testing.T) {
	t.Parallel()
	registered := []string{"http://127.0.0.1/oauth2redirect?provider=example"}
	candidate := "http://127.0.0.1:51004/oauth2redirect?provider=example"
	if got := pickRegistered(registered, candidate, true); got != candidate {
		t.Fatalf("public loopback redirect = %q, want %q", got, candidate)
	}
	for _, rejected := range []string{
		"http://127.0.0.1:51004/other?provider=example",
		"http://127.0.0.2:51004/oauth2redirect?provider=example",
		"http://127.0.0.1:51004/oauth2redirect?provider=other",
	} {
		if got := pickRegistered(registered, rejected, true); got != "" {
			t.Errorf("loopback redirect %q matched as %q", rejected, got)
		}
	}
	if got := pickRegistered(registered, candidate, false); got != "" {
		t.Fatalf("confidential loopback port variance matched as %q", got)
	}
}

func TestAuthorizedScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		requested string
		allowed   []string
		want      string
		wantOK    bool
	}{
		{name: "empty", allowed: []string{"openid"}, wantOK: true},
		{name: "deduplicated", requested: "openid profile openid", allowed: []string{"openid", "profile"}, want: "openid profile", wantOK: true},
		{name: "not allowed", requested: "openid admin", allowed: []string{"openid"}},
		{name: "none allowed", requested: "openid"},
		{name: "tab separator", requested: "openid\tprofile", allowed: []string{"openid", "profile"}},
		{name: "repeated space", requested: "openid  profile", allowed: []string{"openid", "profile"}},
		{name: "leading space", requested: " openid", allowed: []string{"openid"}},
		{name: "non ASCII", requested: "profilé", allowed: []string{"profilé"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := authorizedScopes(test.requested, test.allowed)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("authorizedScopes() = %q, %v; want %q, %v", got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestValidateClientScopeUsesRFC6749Grammar(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"read", "!", "a#[]~"} {
		if err := validateClientScope(valid); err != nil {
			t.Errorf("validateClientScope(%q) error = %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "read write", "read\twrite", `quote"`, `back\\slash`, "nul\x00", "profilé"} {
		if err := validateClientScope(invalid); err == nil {
			t.Errorf("validateClientScope(%q) error = nil", invalid)
		}
	}
}
