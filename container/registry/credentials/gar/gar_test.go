// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gar_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/golusoris/golusoris/container/registry/credentials"
	"github.com/golusoris/golusoris/container/registry/credentials/gar"
)

func TestIsGoogleHost(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"europe-west3-docker.pkg.dev": true,
		"us-docker.pkg.dev":           true,
		"gcr.io":                      true,
		"eu.gcr.io":                   true,
		"docker.pkg.dev.evil.io":      false,
		"notgcr.io":                   false,
		"ghcr.io":                     false,
	}
	for host, want := range cases {
		if got := gar.IsGoogleHost(host); got != want {
			t.Errorf("IsGoogleHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// tokenEndpoint fakes the OAuth2 token endpoint of a service-account JWT
// exchange, answering status/body.
func tokenEndpoint(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("assertion") == "" {
			t.Errorf("token request without JWT assertion: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// serviceAccountJSON returns a service-account key file body whose token
// exchange goes to tokenURL.
func serviceAccountJSON(t *testing.T, tokenURL string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	sa, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "p",
		"private_key_id": "k1",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email":   "ci@p.iam.gserviceaccount.com",
		"token_uri":      tokenURL,
	})
	if err != nil {
		t.Fatalf("marshal service account: %v", err)
	}
	return sa
}

func serviceAccountTokens(t *testing.T, status int, body string) oauth2.TokenSource {
	t.Helper()
	sa := serviceAccountJSON(t, tokenEndpoint(t, status, body))
	creds, err := google.CredentialsFromJSONWithType(context.Background(), sa, google.ServiceAccount, gar.Scope)
	if err != nil {
		t.Fatalf("CredentialsFromJSON: %v", err)
	}
	return creds.TokenSource
}

const okToken = `{"access_token":"ya29.fake","token_type":"Bearer","expires_in":3600}`

func TestCredential(t *testing.T) {
	t.Parallel()
	p := gar.New(serviceAccountTokens(t, http.StatusOK, okToken))
	got, err := p.Credential(t.Context(), "europe-west3-docker.pkg.dev")
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if got.Username != gar.Username || got.Password != "ya29.fake" || got.ExpiresAt.IsZero() {
		t.Fatalf("credential = %+v", got)
	}
	if _, err = p.Credential(t.Context(), "ghcr.io"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("non-Google host err = %v", err)
	}
}

func TestCredential_Failures(t *testing.T) {
	t.Parallel()
	cases := map[string]oauth2.TokenSource{
		"endpoint rejects": serviceAccountTokens(t, http.StatusUnauthorized, `{"error":"invalid_grant"}`),
		"empty token":      oauth2.StaticTokenSource(&oauth2.Token{TokenType: "Bearer"}),
	}
	for name, ts := range cases {
		_, err := gar.New(ts).Credential(t.Context(), "gcr.io")
		if err == nil || errors.Is(err, credentials.ErrNoCredential) {
			t.Errorf("%s: err = %v, want hard error", name, err)
		}
	}
}

// TestModule_ADC points Application Default Credentials at a key file.
func TestModule_ADC(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(keyFile, serviceAccountJSON(t, tokenEndpoint(t, http.StatusOK, okToken)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", keyFile)
	var providers []credentials.Provider
	app := fxtest.New(t, gar.Module, fx.Invoke(fx.Annotate(
		func(ps []credentials.Provider) { providers = ps }, fx.ParamTags(credentials.Group))))
	app.RequireStart()
	defer app.RequireStop()
	if len(providers) != 1 {
		t.Fatalf("group has %d providers, want 1", len(providers))
	}
	got, err := providers[0].Credential(t.Context(), "us-docker.pkg.dev")
	if err != nil || got.Password != "ya29.fake" {
		t.Fatalf("credential = %+v, %v", got, err)
	}
}

// TestNewDefault_NoADC breaks discovery with a missing key file: the lookup
// fails loudly instead of degrading to anonymous access.
func TestNewDefault_NoADC(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
	_, err := gar.NewDefault().Credential(t.Context(), "gcr.io")
	if err == nil || errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("err = %v, want discovery error", err)
	}
}
