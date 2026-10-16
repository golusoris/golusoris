// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oidc_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/oidc"
	gerr "github.com/golusoris/golusoris/core/errors"
)

type mockOIDCServer struct {
	server         *httptest.Server
	rsaKey         *rsa.PrivateKey
	badKey         *rsa.PrivateKey
	discoveryCount atomic.Int64
}

func startMockOIDCServer(t *testing.T) *mockOIDCServer {
	t.Helper()
	return startMockOIDCServerWithListener(t, nil)
}

func startMockOIDCServerWithListener(t *testing.T, listener net.Listener) *mockOIDCServer {
	t.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	badKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	mockServer := &mockOIDCServer{
		rsaKey: rsaKey,
		badKey: badKey,
	}

	var serverURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			mockServer.discoveryCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                serverURL,
				"authorization_endpoint":                serverURL + "/auth",
				"token_endpoint":                        serverURL + "/token",
				"userinfo_endpoint":                     serverURL + "/userinfo",
				"jwks_uri":                              serverURL + "/jwks",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			nStr := base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes())
			eBytes := big.NewInt(int64(rsaKey.E)).Bytes()
			eStr := base64.RawURLEncoding.EncodeToString(eBytes)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{
					{
						"kty": "RSA",
						"alg": "RS256",
						"use": "sig",
						"kid": "key-1",
						"n":   nStr,
						"e":   eStr,
					},
				},
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			code := r.FormValue("code")
			switch code {
			case "err-400":
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
			case "missing-id-token":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "acc-token-no-id",
					"token_type":   "Bearer",
				})
			default:
				signKey := rsaKey
				aud := "test-client"
				if code == "bad-sig" {
					signKey = badKey
				}
				if code == "bad-aud" {
					aud = "wrong-client"
				}

				// go-oidc checks exp against the wall clock.
				now := clockwork.NewRealClock().Now()
				claims := jwt.MapClaims{
					"iss":   serverURL,
					"sub":   "user-42",
					"aud":   aud,
					"exp":   now.Add(time.Hour).Unix(),
					"iat":   now.Unix(),
					"email": "alice@example.com",
				}
				if after, ok := strings.CutPrefix(code, "nonce:"); ok {
					claims["nonce"] = after
				}
				tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
				tok.Header["kid"] = "key-1"
				idTok, signErr := tok.SignedString(signKey)
				if signErr != nil {
					http.Error(w, signErr.Error(), http.StatusInternalServerError)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "valid-access-token",
					"token_type":    "Bearer",
					"expires_in":    3600,
					"refresh_token": "valid-refresh-token",
					"id_token":      idTok,
				})
			}
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer valid-access-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_token"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":        "user-42",
				"email":      "alice@example.com",
				"name":       "Alice W",
				"picture":    "https://example.com/pic.png",
				"department": "eng",
			})
		default:
			http.NotFound(w, r)
		}
	})

	var server *httptest.Server
	if listener == nil {
		server = httptest.NewServer(handler)
	} else {
		server = &httptest.Server{
			Listener: listener,
			Config:   &http.Server{Handler: handler},
		}
		server.Start()
	}
	t.Cleanup(server.Close)
	serverURL = server.URL

	mockServer.server = server
	return mockServer
}

func newTestProvider(t *testing.T, issuerURL string) *oidc.Provider {
	t.Helper()
	p, err := oidc.NewProvider(t.Context(), oidc.Options{
		IssuerURL:        issuerURL,
		ClientID:         "test-client",
		RedirectURL:      "https://app.example.com/callback",
		DiscoveryTimeout: 5 * time.Second,
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	return p
}

func requireUnauthorized(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var coded *gerr.Error
	require.True(t, errors.As(err, &coded), "error must wrap *gerr.Error: %v", err)
	require.Equal(t, gerr.CodeUnauthorized, coded.Code)
}

func TestProvider_Exchange(t *testing.T) {
	t.Parallel()
	mock := startMockOIDCServer(t)
	provider := newTestProvider(t, mock.server.URL)
	ctx := t.Context()

	// Positive: valid exchange returns full token set
	tokens, err := provider.Exchange(ctx, "valid", "test-verifier")
	require.NoError(t, err)
	require.Equal(t, "valid-access-token", tokens.AccessToken)
	require.Equal(t, "valid-refresh-token", tokens.RefreshToken)
	require.NotEmpty(t, tokens.IDToken)
	require.Equal(t, "user-42", tokens.Subject)
	require.Empty(t, tokens.Nonce)

	var claims struct {
		Email string `json:"email"`
	}
	require.NoError(t, tokens.Claims(&claims))
	require.Equal(t, "alice@example.com", claims.Email)

	var zeroTokens oidc.TokenSet
	require.EqualError(t, zeroTokens.Claims(&claims), "auth/oidc: no verified id_token")

	// Negative: token endpoint returns 400
	_, err = provider.Exchange(ctx, "err-400", "test-verifier")
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth/oidc: exchange")

	// Negative: response missing id_token
	_, err = provider.Exchange(ctx, "missing-id-token", "test-verifier")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no id_token in response")

	// Negative: id_token with untrusted signature
	_, err = provider.Exchange(ctx, "bad-sig", "test-verifier")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid id_token")

	// Negative: id_token with wrong audience
	_, err = provider.Exchange(ctx, "bad-aud", "test-verifier")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid id_token")
}

func TestProvider_Begin(t *testing.T) {
	t.Parallel()
	mock := startMockOIDCServer(t)
	provider := newTestProvider(t, mock.server.URL)
	ctx := t.Context()

	// Empty state -> error
	_, err := provider.Begin(ctx, "")
	require.EqualError(t, err, "auth/oidc: state must not be empty")

	// Call 1: valid URL containing nonce and code_challenge, nonce 43 chars
	req1, err := provider.Begin(ctx, "state-1")
	require.NoError(t, err)
	require.Len(t, req1.Nonce, 43)
	require.Len(t, req1.Verifier, 43)
	require.Contains(t, req1.URL, "nonce="+req1.Nonce)
	require.Contains(t, req1.URL, "code_challenge=")
	require.Contains(t, req1.URL, "state=state-1")

	// Call 2: different nonces and verifiers
	req2, err := provider.Begin(ctx, "state-2")
	require.NoError(t, err)
	require.Len(t, req2.Nonce, 43)
	require.NotEqual(t, req1.Nonce, req2.Nonce)
	require.NotEqual(t, req1.Verifier, req2.Verifier)
}

func TestProvider_Complete(t *testing.T) {
	t.Parallel()
	mock := startMockOIDCServer(t)
	provider := newTestProvider(t, mock.server.URL)
	ctx := t.Context()

	req, err := provider.Begin(ctx, "test-state")
	require.NoError(t, err)

	// Happy path: fake IdP signing id_token carrying the nonce
	tokens, err := provider.Complete(ctx, "nonce:"+req.Nonce, req)
	require.NoError(t, err)
	require.Equal(t, "user-42", tokens.Subject)
	require.Equal(t, req.Nonce, tokens.Nonce)
	var claims struct {
		Email string `json:"email"`
	}
	require.NoError(t, tokens.Claims(&claims))
	require.Equal(t, "alice@example.com", claims.Email)

	// Nonce mismatch -> unauthorized
	_, err = provider.Complete(ctx, "nonce:wrong-nonce", req)
	requireUnauthorized(t, err)
	require.Contains(t, err.Error(), "nonce mismatch")

	// id_token without nonce -> unauthorized
	_, err = provider.Complete(ctx, "valid", req)
	requireUnauthorized(t, err)
	require.Contains(t, err.Error(), "nonce mismatch")

	// req.Nonce empty -> error
	emptyNonceReq := req
	emptyNonceReq.Nonce = ""
	_, err = provider.Complete(ctx, "nonce:"+req.Nonce, emptyNonceReq)
	requireUnauthorized(t, err)
}

func TestProvider_LazyDiscovery(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	provider, err := oidc.NewProvider(ctx, oidc.Options{
		IssuerURL:        "http://" + addr,
		ClientID:         "test-client",
		RedirectURL:      "https://app.example.com/callback",
		DiscoveryTimeout: 200 * time.Millisecond,
		LazyDiscovery:    true,
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.NotNil(t, provider)

	_, _, err = provider.AuthURL("test-state")
	require.EqualError(t, err, "auth/oidc: lazy provider not discovered; use Begin")

	clock := clockwork.NewRealClock()
	start := clock.Now()
	_, err = provider.Begin(ctx, "test-state")
	elapsed := clock.Since(start)
	require.Error(t, err)
	require.Less(t, elapsed, 2*time.Second)

	newListener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	_ = startMockOIDCServerWithListener(t, newListener)

	req, err := provider.Begin(ctx, "test-state")
	require.NoError(t, err)
	require.NotEmpty(t, req.URL)

	authURL, verifier, err := provider.AuthURL("test-state")
	require.NoError(t, err)
	require.NotEmpty(t, authURL)
	require.NotEmpty(t, verifier)
}

func TestProvider_LazyDiscovery_ConcurrentBegin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mock := startMockOIDCServer(t)

	provider, err := oidc.NewProvider(ctx, oidc.Options{
		IssuerURL:        mock.server.URL,
		ClientID:         "test-client",
		RedirectURL:      "https://app.example.com/callback",
		DiscoveryTimeout: 5 * time.Second,
		LazyDiscovery:    true,
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	mock.discoveryCount.Store(0)
	const concurrentRoutines = 8
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentRoutines)

	for i := range concurrentRoutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, bErr := provider.Begin(ctx, fmt.Sprintf("state-%d", idx))
			errCh <- bErr
		}(i)
	}
	wg.Wait()
	close(errCh)

	for bErr := range errCh {
		require.NoError(t, bErr)
	}
	require.Equal(t, int64(1), mock.discoveryCount.Load())
}

func TestProvider_UserInfo(t *testing.T) {
	t.Parallel()
	mock := startMockOIDCServer(t)
	provider := newTestProvider(t, mock.server.URL)
	ctx := t.Context()

	// Positive: valid access token returns claims
	info, err := provider.UserInfo(ctx, "valid-access-token")
	require.NoError(t, err)
	require.Equal(t, "user-42", info.Subject)
	require.Equal(t, "alice@example.com", info.Email)
	require.Equal(t, "Alice W", info.Name)
	require.Equal(t, "https://example.com/pic.png", info.Picture)
	require.Equal(t, "eng", info.Extra["department"])

	// Negative: invalid access token returns error from 401
	_, err = provider.UserInfo(ctx, "invalid-token")
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth/oidc: userinfo")
}
