// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oidc_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/oidc"
)

type mockOIDCServer struct {
	server *httptest.Server
	rsaKey *rsa.PrivateKey
	badKey *rsa.PrivateKey
}

func startMockOIDCServer(t *testing.T) *mockOIDCServer {
	t.Helper()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	badKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	var serverURL string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
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
					"iss": serverURL,
					"sub": "user-42",
					"aud": aud,
					"exp": now.Add(time.Hour).Unix(),
					"iat": now.Unix(),
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

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	serverURL = server.URL

	return &mockOIDCServer{
		server: server,
		rsaKey: rsaKey,
		badKey: badKey,
	}
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
