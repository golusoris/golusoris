// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oidc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/golusoris/golusoris/core/config"
)

func TestOptions_withDefaults_addsDefaultScopes(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	require.Contains(t, got.Scopes, "openid")
	require.Contains(t, got.Scopes, "email")
	require.Contains(t, got.Scopes, "profile")
}

func TestOptions_withDefaults_preservesExplicitScopes(t *testing.T) {
	t.Parallel()
	got := Options{Scopes: []string{"openid", "groups"}}.withDefaults()
	require.Equal(t, []string{"openid", "groups"}, got.Scopes)
}

func TestOptions_withDefaults_requiresOpenIDAndClonesScopes(t *testing.T) {
	t.Parallel()
	scopes := []string{"groups"}
	got := (Options{Scopes: scopes}).withDefaults()
	scopes[0] = "mutated"
	require.Equal(t, []string{"openid", "groups"}, got.Scopes)
}

func TestProvider_AuthURLRejectsEmptyState(t *testing.T) {
	t.Parallel()
	provider := &Provider{cfg: oauth2.Config{
		ClientID: "client",
		Endpoint: oauth2.Endpoint{AuthURL: "https://issuer.example.test/authorize"},
	}}
	authURL, verifier, err := provider.AuthURL("")
	require.Error(t, err)
	require.Empty(t, authURL)
	require.Empty(t, verifier)

	authURL, _, err = provider.AuthURL("opaque-state")
	require.NoError(t, err)
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, "opaque-state", parsed.Query().Get("state"))
}

func TestOptions_withDefaults_boundsInjectedHTTPClient(t *testing.T) {
	t.Parallel()
	client := &http.Client{}
	got := (Options{DiscoveryTimeout: 25 * time.Millisecond, HTTPClient: client}).withDefaults()

	require.Equal(t, 25*time.Millisecond, got.DiscoveryTimeout)
	require.Equal(t, 25*time.Millisecond, got.HTTPClient.Timeout)
	require.Zero(t, client.Timeout, "defaults must not mutate the caller's client")
}

func TestOptions_withDefaults_clonesBoundedHTTPClient(t *testing.T) {
	t.Parallel()
	client := &http.Client{Timeout: time.Minute}
	got := (Options{DiscoveryTimeout: time.Second, HTTPClient: client}).withDefaults()

	require.NotSame(t, client, got.HTTPClient)
	require.Equal(t, time.Minute, got.HTTPClient.Timeout)
	client.Timeout = 2 * time.Minute
	require.Equal(t, time.Minute, got.HTTPClient.Timeout)
}

func TestLoadOptions_appliesDefaultsOnEmpty(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	require.NoError(t, err)
	opts, err := loadOptions(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, opts.Scopes, "defaults should populate Scopes")
	require.Equal(t, defaultDiscoveryTimeout, opts.DiscoveryTimeout)
	require.Equal(t, defaultDiscoveryTimeout, opts.HTTPClient.Timeout)
}

// TestPKCEVerifier_isRFC7636Compliant: RFC 7636 §4.1 specifies the
// verifier as 43-128 chars of [A-Z/a-z/0-9/-/./_/~]. base64.RawURLEncoding
// of 32 bytes yields 43 chars with an unreserved alphabet.
func TestPKCEVerifier_isRFC7636Compliant(t *testing.T) {
	t.Parallel()
	v, err := pkceVerifier()
	require.NoError(t, err)
	require.Len(t, v, 43, "RFC 7636: 43 chars from 32 random bytes")
	decoded, err := base64.RawURLEncoding.DecodeString(v)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
}

func TestPKCEVerifier_unique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 32)
	for range 32 {
		v, err := pkceVerifier()
		require.NoError(t, err)
		seen[v] = struct{}{}
	}
	require.Len(t, seen, 32, "verifiers must be unique across 32 calls")
}

// TestPKCEChallenge_deterministic: RFC 7636 §4.2 S256 method — challenge
// is base64url(sha256(verifier)).
func TestPKCEChallenge_deterministic(t *testing.T) {
	t.Parallel()
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 Appendix B uses this verifier and expects this challenge.
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	got := pkceChallenge(verifier)
	require.Equal(t, want, got)
}

func TestNewProvider_DiscoveryUsesInjectedDeadlineContext(t *testing.T) {
	t.Parallel()

	var deadlineRemaining time.Duration
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if ok {
			deadlineRemaining = time.Until(deadline)
		}
		return nil, errors.New("stop after deadline inspection")
	}), Timeout: time.Minute}
	provider, err := NewProvider(context.Background(), Options{
		IssuerURL:        "https://issuer.example.test",
		ClientID:         "client-id",
		RedirectURL:      "https://app.example.test/callback",
		DiscoveryTimeout: time.Second,
		HTTPClient:       client,
	}, slog.New(slog.DiscardHandler))

	require.Error(t, err)
	require.Nil(t, provider)
	require.Positive(t, deadlineRemaining)
	require.LessOrEqual(t, deadlineRemaining, time.Second)
	require.Equal(t, time.Minute, client.Timeout)
}

func TestNewProvider_UsesInjectedHTTPClient(t *testing.T) {
	t.Parallel()

	requestSeen := make(chan bool, 1)
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestSeen <- request.Header.Get("X-Test-Client") == "injected"
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/authorize",
			"token_endpoint":         issuer + "/token",
			"jwks_uri":               issuer + "/jwks",
		}); err != nil {
			return
		}
	}))
	t.Cleanup(server.Close)
	issuer = server.URL
	baseTransport := server.Client().Transport
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.Header.Set("X-Test-Client", "injected")
		return baseTransport.RoundTrip(request)
	})}

	provider, err := NewProvider(t.Context(), Options{
		IssuerURL:        issuer,
		ClientID:         "client-id",
		RedirectURL:      "https://app.example.test/callback",
		DiscoveryTimeout: time.Second,
		HTTPClient:       client,
	}, slog.New(slog.DiscardHandler))

	require.NoError(t, err)
	require.NotNil(t, provider)
	require.True(t, <-requestSeen)
	require.Equal(t, time.Second, provider.client.Timeout)
	require.Zero(t, client.Timeout)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
