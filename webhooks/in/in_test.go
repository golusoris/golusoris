// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package in_test

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // test helper for GitHubLegacy (SHA-1 by spec)
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/webhooks/in"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

type statusHandler struct {
	status int
}

func (h *statusHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(h.status)
}

func requireMiddleware(
	t *testing.T,
	middleware func(http.Handler) http.Handler,
	err error,
) func(http.Handler) http.Handler {
	t.Helper()
	if err != nil {
		t.Fatalf("middleware constructor: %v", err)
	}
	return middleware
}

func requireGitHub(t *testing.T, secret string) func(http.Handler) http.Handler {
	t.Helper()
	middleware, err := in.GitHub(secret)
	return requireMiddleware(t, middleware, err)
}

func requireStripe(t *testing.T, secret string) func(http.Handler) http.Handler {
	t.Helper()
	middleware, err := in.Stripe(secret)
	return requireMiddleware(t, middleware, err)
}

func requireSlack(t *testing.T, secret string) func(http.Handler) http.Handler {
	t.Helper()
	middleware, err := in.Slack(secret)
	return requireMiddleware(t, middleware, err)
}

func requireGitHubLegacy(t *testing.T, secret string) func(http.Handler) http.Handler {
	t.Helper()
	middleware, err := in.GitHubLegacy(secret)
	return requireMiddleware(t, middleware, err)
}

func requireHMAC(t *testing.T, secret, header string) func(http.Handler) http.Handler {
	t.Helper()
	middleware, err := in.HMAC(secret, header)
	return requireMiddleware(t, middleware, err)
}

// --- GitHub ---

func githubSig(secret, body string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

func TestMiddlewaresRejectNilDownstream(t *testing.T) {
	t.Parallel()
	const (
		secret = "shared-secret"
		body   = `{"event":"test"}`
	)
	timestamp := time.Now().Unix()
	tests := []struct {
		name       string
		middleware func(http.Handler) http.Handler
		request    func() *http.Request
	}{
		{
			name:       "Stripe",
			middleware: requireStripe(t, secret),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				req.Header.Set("Stripe-Signature", stripeSig(secret, body, timestamp))
				return req
			},
		},
		{
			name:       "GitHub",
			middleware: requireGitHub(t, secret),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				req.Header.Set("X-Hub-Signature-256", githubSig(secret, body))
				return req
			},
		},
		{
			name:       "GitHub legacy",
			middleware: requireGitHubLegacy(t, secret),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				req.Header.Set("X-Hub-Signature", githubLegacySig(secret, body))
				return req
			},
		},
		{
			name:       "Slack",
			middleware: requireSlack(t, secret),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(timestamp, 10))
				req.Header.Set("X-Slack-Signature", slackSig(secret, body, timestamp))
				return req
			},
		},
		{
			name:       "generic HMAC",
			middleware: requireHMAC(t, secret, "X-Signature"),
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				req.Header.Set("X-Signature", githubSig(secret, body))
				return req
			},
		},
	}
	var typedNil *statusHandler
	downstreams := []struct {
		name    string
		handler http.Handler
	}{
		{name: "nil", handler: nil},
		{name: "typed nil", handler: typedNil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, downstream := range downstreams {
				t.Run(downstream.name, func(t *testing.T) {
					t.Parallel()
					rec := httptest.NewRecorder()

					test.middleware(downstream.handler).ServeHTTP(rec, test.request())

					if rec.Code != http.StatusInternalServerError {
						t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
					}
				})
			}
		})
	}
}

func TestGitHub_valid(t *testing.T) {
	t.Parallel()
	const secret = "mysecret"
	body := `{"action":"push"}`
	handler := requireGitHub(t, secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSig(secret, body))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestGitHub_invalid(t *testing.T) {
	t.Parallel()
	handler := requireGitHub(t, "secret")(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestGitHub_missingHeader(t *testing.T) {
	t.Parallel()
	handler := requireGitHub(t, "secret")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

// --- Stripe ---

func stripeSig(secret, body string, ts int64) string {
	payload := fmt.Sprintf("%d.%s", ts, body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	mac := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", ts, mac)
}

func TestStripe_valid(t *testing.T) {
	t.Parallel()
	const secret = "whsec_test"
	body := `{"type":"charge.succeeded"}`
	ts := time.Now().Unix()
	handler := requireStripe(t, secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", stripeSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestStripe_oldTimestamp(t *testing.T) {
	t.Parallel()
	const secret = "whsec_test"
	body := `{}`
	ts := time.Now().Add(-10 * time.Minute).Unix()
	handler := requireStripe(t, secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", stripeSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestStripe_futureTimestamp(t *testing.T) {
	t.Parallel()
	const secret = "whsec_test"
	body := `{}`
	ts := time.Now().Add(10 * time.Minute).Unix()
	handler := requireStripe(t, secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", stripeSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

// TestStripe_malformedHeader covers the parseStripeSignatureHeader boundary
// where required keys ("t", "v1") are absent from an otherwise well-formed
// header.
func TestStripe_malformedHeader(t *testing.T) {
	t.Parallel()
	handler := requireStripe(t, "whsec_test")(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("Stripe-Signature", "foo=bar,baz") // no t=, no v1=
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

// TestStripe_wrongSignature is the negative case for anyHMACMatches: a
// well-formed, fresh header whose v1 digest simply does not match.
func TestStripe_wrongSignature(t *testing.T) {
	t.Parallel()
	const secret = "whsec_test"
	body := `{"type":"charge.succeeded"}`
	ts := time.Now().Unix()
	handler := requireStripe(t, secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=%s", ts, "deadbeef"))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

// TestStripe_secondSignatureMatches covers anyHMACMatches scanning past a
// non-matching v1 entry (e.g. during a Stripe signing-secret rotation, which
// sends multiple v1 values) to find one that does.
func TestStripe_secondSignatureMatches(t *testing.T) {
	t.Parallel()
	const secret = "whsec_rotation"
	body := `{"type":"charge.succeeded"}`
	ts := time.Now().Unix()
	valid := stripeSig(secret, body, ts) // "t=<ts>,v1=<mac>"
	_, correctMac, _ := strings.Cut(valid, "v1=")
	header := fmt.Sprintf("t=%d,v1=deadbeef,v1=%s", ts, correctMac) // wrong sig first, correct one second

	handler := requireStripe(t, secret)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

// --- Slack ---

func slackSig(secret, body string, ts int64) string {
	base := fmt.Sprintf("v0:%d:%s", ts, body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(base))
	return "v0=" + hex.EncodeToString(h.Sum(nil))
}

func TestSlack_valid(t *testing.T) {
	t.Parallel()
	const secret = "slack_secret"
	body := `payload=test`
	ts := time.Now().Unix()
	handler := requireSlack(t, secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Slack-Signature", slackSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestSlack_futureTimestamp(t *testing.T) {
	t.Parallel()
	const secret = "slack_secret"
	body := `payload=test`
	ts := time.Now().Add(10 * time.Minute).Unix()
	handler := requireSlack(t, secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Slack-Signature", slackSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestOversizedBodyIsRejectedInsteadOfVerifyingTruncatedPrefix(t *testing.T) {
	t.Parallel()
	const secret = "genericsecret"
	body := strings.Repeat("a", in.MaxBodyBytes+1)
	prefix := body[:in.MaxBodyBytes]
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	handler := requireGitHub(t, secret)(next)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSig(secret, prefix))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rw.Code)
	}
	if called {
		t.Fatal("oversized body reached downstream handler")
	}
}

func TestSlackOversizedBodyReturnsRequestEntityTooLarge(t *testing.T) {
	t.Parallel()
	handler := requireSlack(t, "slack_secret")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", in.MaxBodyBytes+1)))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rw.Code)
	}
}

func TestConstructorsRejectEmptySecrets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		build func() (func(http.Handler) http.Handler, error)
	}{
		{name: "stripe", build: func() (func(http.Handler) http.Handler, error) { return in.Stripe("") }},
		{name: "github", build: func() (func(http.Handler) http.Handler, error) { return in.GitHub("") }},
		{name: "github legacy", build: func() (func(http.Handler) http.Handler, error) { return in.GitHubLegacy("") }},
		{name: "slack", build: func() (func(http.Handler) http.Handler, error) { return in.Slack("") }},
		{name: "generic", build: func() (func(http.Handler) http.Handler, error) { return in.HMAC("", "X-Sig") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			middleware, err := tc.build()
			if err == nil {
				t.Fatal("constructor accepted empty secret")
			}
			if middleware != nil {
				t.Fatal("constructor returned middleware with an error")
			}
		})
	}
}

// --- Generic HMAC ---

func TestHMAC_valid(t *testing.T) {
	t.Parallel()
	const secret = "genericsecret"
	body := `{"event":"test"}`
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(h.Sum(nil))

	handler := requireHMAC(t, secret, "X-My-Signature")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-My-Signature", sig)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

// --- GitHubLegacy (SHA-1) ---

func githubLegacySig(secret, body string) string {
	h := hmac.New(sha1.New, []byte(secret))
	h.Write([]byte(body))
	return "sha1=" + hex.EncodeToString(h.Sum(nil))
}

func TestGitHubLegacy_valid(t *testing.T) {
	t.Parallel()
	const secret = "legacysecret"
	body := `{"ref":"refs/heads/main"}`
	sig := githubLegacySig(secret, body)

	handler := requireGitHubLegacy(t, secret)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature", sig)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestGitHubLegacy_invalid(t *testing.T) {
	t.Parallel()
	handler := requireGitHubLegacy(t, "secret")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("X-Hub-Signature", "sha1=badhex!!!")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestGitHubLegacy_missingHeader(t *testing.T) {
	t.Parallel()
	handler := requireGitHubLegacy(t, "secret")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestHMAC_missingHeader(t *testing.T) {
	t.Parallel()
	handler := requireHMAC(t, "secret", "X-Sig")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestSignatureMiddlewaresRejectWrongAlgorithmPrefix(t *testing.T) {
	t.Parallel()
	const secret = "shared-secret"
	const body = `{"event":"test"}`
	tests := []struct {
		name       string
		headerName string
		header     string
		middleware func(http.Handler) http.Handler
	}{
		{
			name:       "GitHub SHA-256",
			headerName: "X-Hub-Signature-256",
			header:     strings.Replace(githubSig(secret, body), "sha256=", "sha1=", 1),
			middleware: requireGitHub(t, secret),
		},
		{
			name:       "GitHub legacy SHA-1",
			headerName: "X-Hub-Signature",
			header:     strings.Replace(githubLegacySig(secret, body), "sha1=", "sha256=", 1),
			middleware: requireGitHubLegacy(t, secret),
		},
		{
			name:       "generic HMAC SHA-256",
			headerName: "X-Signature",
			header:     strings.Replace(githubSig(secret, body), "sha256=", "md5=", 1),
			middleware: requireHMAC(t, secret, "X-Signature"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(test.headerName, test.header)
			response := httptest.NewRecorder()
			test.middleware(okHandler()).ServeHTTP(response, req)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestSlack_expiredTimestamp(t *testing.T) {
	t.Parallel()
	const secret = "slacksecret"
	body := `payload=test`
	// Timestamp 10 minutes in the past.
	ts := time.Now().Add(-10 * time.Minute).Unix()
	sig := slackSig(secret, body, ts)

	handler := requireSlack(t, secret)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Slack-Signature", sig)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}
