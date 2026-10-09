// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/container/registry/sign"
)

const githubBearer = "runtime-bearer-secret"

func writeToken(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileToken(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	p := filepath.Join(t.TempDir(), "oidc-token")
	writeToken(t, p, "first.jwt.sig\n")
	src := sign.FileToken{Path: p}
	if tok, err := src.Token(ctx); err != nil || tok != "first.jwt.sig" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	writeToken(t, p, "rotated.jwt.sig")
	if tok, err := src.Token(ctx); err != nil || tok != "rotated.jwt.sig" {
		t.Fatalf("Token after rotation = %q, %v", tok, err)
	}
	limit := 64 << 10
	writeToken(t, p, strings.Repeat("a", limit))
	if tok, err := src.Token(ctx); err != nil || len(tok) != limit {
		t.Fatalf("Token at the size cap = %d bytes, %v", len(tok), err)
	}
	writeToken(t, p, strings.Repeat("s", limit+1))
	if _, err := src.Token(ctx); !errors.Is(err, sign.ErrInvalidOptions) || strings.Contains(err.Error(), "sss") {
		t.Fatalf("oversized token err = %v", err)
	}
	writeToken(t, p, " \n")
	if _, err := src.Token(ctx); !errors.Is(err, sign.ErrNoToken) {
		t.Fatalf("blank token err = %v", err)
	}
	if _, err := (sign.FileToken{Path: p + ".missing"}).Token(ctx); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file err = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := src.Token(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx err = %v", err)
	}
	if _, err := os.Stat(sign.DefaultTokenPath); err == nil {
		t.Skip("host has a token at the default path")
	}
	if _, err := (sign.FileToken{}).Token(ctx); err == nil || !strings.Contains(err.Error(), filepath.FromSlash(sign.DefaultTokenPath)) {
		t.Fatalf("default path err = %v", err)
	}
}

// githubRuntime fakes the Actions OIDC endpoint: it checks the runtime bearer
// and returns respond's body for the requested audience.
func githubRuntime(t *testing.T, respond func(w http.ResponseWriter, audience string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "bearer "+githubBearer || r.URL.Query().Get("api-version") != "2.0" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		respond(w, r.URL.Query().Get("audience"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setGitHubEnv(t *testing.T, srv *httptest.Server, bearer string) {
	t.Helper()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/idtoken?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", bearer)
}

func tokenJSON(w http.ResponseWriter, value string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "value": value})
}

func TestGitHubToken(t *testing.T) {
	srv := githubRuntime(t, func(w http.ResponseWriter, audience string) { tokenJSON(w, "jwt-for-"+audience) })
	setGitHubEnv(t, srv, githubBearer)
	ctx := testCtx(t)
	tr := newTransport(t)
	if tok, err := (sign.GitHubToken{Transport: tr}).Token(ctx); err != nil || tok != "jwt-for-sigstore" {
		t.Fatalf("default audience = %q, %v", tok, err)
	}
	if tok, err := (sign.GitHubToken{Audience: "vmafx", Transport: tr}).Token(ctx); err != nil || tok != "jwt-for-vmafx" {
		t.Fatalf("custom audience = %q, %v", tok, err)
	}
	setGitHubEnv(t, srv, "wrong-bearer")
	_, err := (sign.GitHubToken{Transport: tr}).Token(ctx)
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "wrong-bearer") {
		t.Fatalf("rejected bearer err = %v", err)
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	if _, err = (sign.GitHubToken{Transport: tr}).Token(ctx); !errors.Is(err, sign.ErrNoToken) {
		t.Fatalf("unset bearer err = %v", err)
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", githubBearer)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	if _, err = (sign.GitHubToken{Transport: tr}).Token(ctx); !errors.Is(err, sign.ErrNoToken) {
		t.Fatalf("unset URL err = %v", err)
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "file:///etc/passwd")
	if _, err = (sign.GitHubToken{Transport: tr}).Token(ctx); !errors.Is(err, sign.ErrInvalidOptions) {
		t.Fatalf("non-http URL err = %v", err)
	}
}

//nolint:paralleltest // t.Setenv sets the GitHub Actions runtime variables.
func TestGitHubToken_BadResponses(t *testing.T) {
	ctx := testCtx(t)
	tr := newTransport(t)
	cases := []struct {
		name    string
		respond func(w http.ResponseWriter, audience string)
		want    error
		msg     string
	}{
		{name: "empty value", respond: func(w http.ResponseWriter, _ string) { tokenJSON(w, "") }, want: sign.ErrNoToken},
		{name: "oversized", respond: func(w http.ResponseWriter, _ string) { tokenJSON(w, strings.Repeat("t", 64<<10)) }, want: sign.ErrInvalidOptions},
		{name: "not json", respond: func(w http.ResponseWriter, _ string) { _, _ = w.Write([]byte("<html>")) }, msg: "not JSON"},
		{name: "server error", respond: func(w http.ResponseWriter, _ string) {
			w.WriteHeader(http.StatusBadGateway)
			tokenJSON(w, "jwt-from-a-failed-request")
		}},
	}
	for _, tc := range cases {
		setGitHubEnv(t, githubRuntime(t, tc.respond), githubBearer)
		_, err := (sign.GitHubToken{Transport: tr}).Token(ctx)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || !strings.Contains(err.Error(), tc.msg) ||
			strings.Contains(err.Error(), githubBearer) || strings.Contains(err.Error(), "ttt") {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	release := make(chan struct{})
	slow := githubRuntime(t, func(http.ResponseWriter, string) { <-release })
	t.Cleanup(func() { close(release) })
	setGitHubEnv(t, slow, githubBearer)
	start := time.Now()
	if _, err := (sign.GitHubToken{Transport: tr, Timeout: 50 * time.Millisecond}).Token(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung runtime err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

// TestGitHubToken_KeylessLayout signs a layout keyless with a token from the
// fake Actions runtime and verifies the signature offline against the
// identity Fulcio certified.
//
//nolint:paralleltest // t.Setenv sets the GitHub Actions runtime variables.
func TestGitHubToken_KeylessLayout(t *testing.T) {
	token := idToken(t)
	srv := githubRuntime(t, func(w http.ResponseWriter, audience string) {
		if audience != sign.DefaultAudience {
			http.Error(w, "audience", http.StatusBadRequest)
			return
		}
		tokenJSON(w, token)
	})
	setGitHubEnv(t, srv, githubBearer)
	ca := newCA(t, "test fulcio")
	l, _, h := layoutImage(t, false)
	opts := sign.Options{FulcioURL: fulcioServer(t, ca, token).URL, Transport: newTransport(t)}
	gh := sign.GitHubToken{Transport: newTransport(t)}
	if _, err := sign.ImageLayout(testCtx(t), l, h, sign.Signer{IDToken: gh.Token}, opts); err != nil {
		t.Fatalf("ImageLayout keyless: %v", err)
	}
	p := sign.Policy{
		Identities:      []sign.Identity{{Issuer: testIssuer, Subject: testEmail}},
		TrustedMaterial: trustRoot(t, &ca, nil, nil), InsecureIgnoreTlog: true, InsecureIgnoreSCT: true,
	}
	got := verifyLayoutOne(t, l, h, p)
	if got.Result.Signature.Certificate == nil || got.Result.Signature.Certificate.SubjectAlternativeName != testEmail {
		t.Fatalf("result = %+v", got.Result)
	}
	p.Identities = []sign.Identity{{Issuer: testIssuer, Subject: "mallory@example.com"}}
	wantLayoutRejected(t, l, h, p, "certificate identity")
}
