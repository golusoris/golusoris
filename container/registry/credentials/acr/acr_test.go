// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package acr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/container/registry/credentials"
	"github.com/golusoris/golusoris/core/config"
)

// fakeEntra is an [azcore.TokenCredential] returning a fixed token.
type fakeEntra struct {
	token string
	err   error
}

func (f fakeEntra) GetToken(_ context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if len(o.Scopes) != 1 || o.Scopes[0] != Scope {
		return azcore.AccessToken{}, errors.New("unexpected scopes")
	}
	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Unix(1900000000, 0)}, f.err
}

const testHost = "contoso.azurecr.io"

// exchangeServer fakes POST /oauth2/exchange; it checks the form and
// answers status/body.
func exchangeServer(t *testing.T, cred azcore.TokenCredential, opts Options, status int, body string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		f := r.PostForm
		if r.Method != http.MethodPost || f.Get("grant_type") != "access_token" || f.Get("service") != testHost ||
			f.Get("access_token") != "entra-token" || f.Get("tenant") != opts.TenantID {
			t.Errorf("unexpected exchange %s %v", r.Method, f)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	p := New(cred, opts, srv.Client())
	p.endpoint = func(string) string { return srv.URL + "/oauth2/exchange" }
	return p
}

func TestCredential(t *testing.T) {
	t.Parallel()
	for _, tenant := range []string{"", "409520d4-8100-4d1d-ad47-72432ddcc120"} {
		p := exchangeServer(t, fakeEntra{token: "entra-token"}, Options{TenantID: tenant}, http.StatusOK, `{"refresh_token":"acr-refresh"}`)
		got, err := p.Credential(t.Context(), testHost)
		if err != nil {
			t.Fatalf("tenant %q: Credential: %v", tenant, err)
		}
		if got.Username != Username || got.Password != "acr-refresh" || !got.ExpiresAt.Equal(time.Unix(1900000000, 0)) {
			t.Fatalf("credential = %+v", got)
		}
	}
}

func TestCredential_Failures(t *testing.T) {
	t.Parallel()
	ok := fakeEntra{token: "entra-token"}
	cases := map[string]*Provider{
		"exchange denied":  exchangeServer(t, ok, Options{}, http.StatusUnauthorized, `{"errors":[]}`),
		"no refresh token": exchangeServer(t, ok, Options{}, http.StatusOK, `{}`),
		"malformed body":   exchangeServer(t, ok, Options{}, http.StatusOK, `{`),
		"entra fails":      exchangeServer(t, fakeEntra{err: errors.New("no identity")}, Options{}, http.StatusOK, `{}`),
	}
	for name, p := range cases {
		_, err := p.Credential(t.Context(), testHost)
		if err == nil || errors.Is(err, credentials.ErrNoCredential) {
			t.Errorf("%s: err = %v, want hard error", name, err)
		}
	}
	if _, err := New(ok, Options{}, nil).Credential(t.Context(), "ghcr.io"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("non-ACR host err = %v", err)
	}
}

func TestIsACRHost(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"contoso.azurecr.io":              true,
		"contoso.azurecr.cn":              true,
		"contoso.azurecr.us":              true,
		"azurecr.io":                      false,
		"contoso.azurecr.io.evil.example": false,
	}
	for host, want := range cases {
		if got := IsACRHost(host); got != want {
			t.Errorf("IsACRHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func testConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{Delimiter: ".", Files: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadOptions(t *testing.T) {
	t.Parallel()
	opts, err := loadOptions(testConfig(t, "container:\n  registry:\n    credentials:\n      acr:\n        tenant_id: t1\n"))
	if err != nil || opts.TenantID != "t1" || opts.Timeout != DefaultTimeout {
		t.Fatalf("options = %+v, %v", opts, err)
	}
	if _, err = loadOptions(testConfig(t, "container:\n  registry:\n    credentials:\n      acr:\n        timeout: soon\n")); err == nil {
		t.Fatal("invalid timeout accepted")
	}
}

func TestModule(t *testing.T) {
	t.Parallel()
	var providers []credentials.Provider
	app := fxtest.New(t,
		fx.Supply(testConfig(t, "container: {}\n")),
		Module,
		fx.Invoke(fx.Annotate(func(ps []credentials.Provider) { providers = ps }, fx.ParamTags(credentials.Group))),
	)
	app.RequireStart()
	defer app.RequireStop()
	if len(providers) != 1 {
		t.Fatalf("group has %d providers, want 1", len(providers))
	}
	if _, err := providers[0].Credential(t.Context(), "ghcr.io"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("non-ACR host err = %v", err)
	}
}
