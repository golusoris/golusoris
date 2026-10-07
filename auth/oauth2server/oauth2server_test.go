// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/jwt"
	"github.com/golusoris/golusoris/auth/oauth2server"
)

func TestNew_RequiresOptions(t *testing.T) {
	t.Parallel()
	_, err := oauth2server.New(oauth2server.Options{Issuer: "iss"})
	require.Error(t, err)
}

func TestNew_RejectsTypedNilStores(t *testing.T) {
	t.Parallel()

	t.Run("clients", func(t *testing.T) {
		t.Parallel()
		opts := validOptions(t)
		var clients *oauth2server.MemoryClientStore
		opts.Clients = clients
		_, err := oauth2server.New(opts)
		require.Error(t, err)
	})

	t.Run("codes", func(t *testing.T) {
		t.Parallel()
		opts := validOptions(t)
		var codes *oauth2server.MemoryCodeStore
		opts.Codes = codes
		_, err := oauth2server.New(opts)
		require.Error(t, err)
	})
}

func TestNewDefaultsTypedNilOptionalClock(t *testing.T) {
	t.Parallel()

	opts := validOptions(t)
	var typedNilClock *clockwork.FakeClock
	opts.Clock = typedNilClock
	server, err := oauth2server.New(opts)
	require.NoError(t, err)

	verifier := "typed-nil-clock-verifier-with-at-least-43-characters"
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {"spa"},
		"redirect_uri":          {"https://app.example/callback"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"scope":                 {"openid"},
	}
	request := httptest.NewRequest(http.MethodGet, "/authorize?"+query.Encode(), nil)
	recorder := httptest.NewRecorder()
	server.Routes().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusFound, recorder.Code, recorder.Body.String())
}

func TestMemoryClientStoreAddPreservesLegacyFunctionType(t *testing.T) {
	t.Parallel()
	store := oauth2server.NewMemoryClientStore()
	add := requireLegacyClientAdd(store.Add)

	add(oauth2server.Client{
		ID:           "legacy",
		RedirectURIs: []string{"https://app.example/callback"},
		PublicClient: true,
	})
	_, err := store.Get(t.Context(), "legacy")
	require.NoError(t, err)

	add(oauth2server.Client{ID: "invalid", PublicClient: true})
	_, err = store.Get(t.Context(), "invalid")
	require.Error(t, err, "legacy Add must fail closed for invalid clients")
}

func requireLegacyClientAdd(add func(oauth2server.Client)) func(oauth2server.Client) {
	return add
}

func TestServer_AuthCodePKCEFlow(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	clients := oauth2server.NewMemoryClientStore()
	require.NoError(t, clients.Register(oauth2server.Client{
		ID:           "spa",
		RedirectURIs: []string{"https://app.example/callback"},
		PublicClient: true,
		Scopes:       []string{"openid", "profile"},
	}))
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-at-least-32-bytes-long"), time.Hour)
	require.NoError(t, err)

	srv, err := oauth2server.New(oauth2server.Options{
		Issuer:       "https://issuer.test",
		Clients:      clients,
		Codes:        oauth2server.NewMemoryCodeStore(),
		Signer:       signer,
		Clock:        clk,
		Authenticate: func(_ *http.Request) string { return "user-1" },
	})
	require.NoError(t, err)

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	verifier := "the-verifier-must-be-43-chars-or-more-aaaaaa"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "spa")
	q.Set("redirect_uri", "https://app.example/callback")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", "xyz")
	q.Set("scope", "openid profile")

	noFollow := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(ts.URL + "/authorize?" + q.Encode())
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	loc := resp.Header.Get("Location")
	parsed, err := url.Parse(loc)
	require.NoError(t, err)
	code := parsed.Query().Get("code")
	require.Equal(t, "xyz", parsed.Query().Get("state"), "state must round-trip through the redirect")
	require.NotEmpty(t, code)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "https://app.example/callback")
	form.Set("client_id", "spa")
	form.Set("code_verifier", verifier)

	tokResp, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, tokResp.StatusCode)
	require.Equal(t, "no-store", tokResp.Header.Get("Cache-Control"))
	require.Equal(t, "no-cache", tokResp.Header.Get("Pragma"))

	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	require.NoError(t, json.NewDecoder(tokResp.Body).Decode(&body))
	require.NoError(t, tokResp.Body.Close())
	require.NotEmpty(t, body.AccessToken)
	require.Equal(t, "Bearer", body.TokenType)
	require.Equal(t, "openid profile", body.Scope)

	// Reusing the code is rejected.
	tokResp2, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, tokResp2.StatusCode)
	require.Equal(t, "no-store", tokResp2.Header.Get("Cache-Control"))
	require.Equal(t, "no-cache", tokResp2.Header.Get("Pragma"))
	require.NoError(t, tokResp2.Body.Close())
}

func TestServer_BadClientSecretDoesNotConsumeAuthorizationCode(t *testing.T) {
	t.Parallel()

	const (
		clientID     = "backend"
		clientSecret = "correct-secret"
		redirectURI  = "https://backend.example/callback"
		codeValue    = "single-use-code"
		verifier     = "confidential-client-pkce-verifier-with-43-chars-minimum"
	)
	clients := oauth2server.NewMemoryClientStore()
	require.NoError(t, clients.Register(oauth2server.Client{
		ID:           clientID,
		RedirectURIs: []string{redirectURI},
		Secret:       clientSecret,
		Scopes:       []string{"openid"},
	}))
	clk := clockwork.NewFakeClock()
	challengeSum := sha256.Sum256([]byte(verifier))
	codes := oauth2server.NewMemoryCodeStore()
	require.NoError(t, codes.Save(t.Context(), oauth2server.Code{
		Value: codeValue,
		Req: oauth2server.AuthRequest{
			ClientID:            clientID,
			UserID:              "user-1",
			Scope:               "openid",
			RedirectURI:         redirectURI,
			CodeChallenge:       base64.RawURLEncoding.EncodeToString(challengeSum[:]),
			CodeChallengeMethod: "S256",
			ExpiresAt:           clk.Now().Add(time.Minute),
		},
	}))
	opts := validOptions(t)
	opts.Clients = clients
	opts.Codes = codes
	opts.Clock = clk
	server, err := oauth2server.New(opts)
	require.NoError(t, err)

	redeem := func(secret string) *httptest.ResponseRecorder {
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("code", codeValue)
		form.Set("redirect_uri", redirectURI)
		form.Set("client_id", clientID)
		form.Set("client_secret", secret)
		form.Set("code_verifier", verifier)
		request := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		server.Routes().ServeHTTP(response, request)
		return response
	}

	badSecret := redeem("wrong-secret")
	require.Equal(t, http.StatusUnauthorized, badSecret.Code, badSecret.Body.String())
	validSecret := redeem(clientSecret)
	require.Equal(t, http.StatusOK, validSecret.Code, validSecret.Body.String())
}

func TestNew_RejectsInvalidTTLs(t *testing.T) {
	t.Parallel()
	for _, ttls := range []struct {
		access time.Duration
		code   time.Duration
	}{
		{access: -time.Second},
		{access: time.Millisecond},
		{code: -time.Nanosecond},
	} {
		opts := validOptions(t)
		opts.AccessTTL = ttls.access
		opts.CodeTTL = ttls.code
		_, err := oauth2server.New(opts)
		require.Error(t, err)
	}
}

func TestServer_EnforcesEndpointMethodsAndTokenCachePolicy(t *testing.T) {
	t.Parallel()
	server, err := oauth2server.New(validOptions(t))
	require.NoError(t, err)
	handler := server.Routes()

	authorizeResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		authorizeResponse,
		httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader("response_type=code")),
	)
	require.Equal(t, http.StatusMethodNotAllowed, authorizeResponse.Code)
	require.Equal(t, http.MethodGet, authorizeResponse.Header().Get("Allow"))

	tokenResponse := httptest.NewRecorder()
	handler.ServeHTTP(tokenResponse, httptest.NewRequest(http.MethodPut, "/token", strings.NewReader("grant_type=authorization_code")))
	require.Equal(t, http.StatusMethodNotAllowed, tokenResponse.Code)
	require.Equal(t, http.MethodPost, tokenResponse.Header().Get("Allow"))
	require.Equal(t, "no-store", tokenResponse.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", tokenResponse.Header().Get("Pragma"))
}

func TestServer_LogsResponseEncodingFailure(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	opts := validOptions(t)
	opts.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	server, err := oauth2server.New(opts)
	require.NoError(t, err)
	request := httptest.NewRequest(
		http.MethodPost,
		"/token",
		strings.NewReader("grant_type=unsupported"),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Routes().ServeHTTP(newFailingResponseWriter(), request)
	require.Contains(t, logs.String(), "encode response failed")
}

func validOptions(t *testing.T) oauth2server.Options {
	t.Helper()
	clients := oauth2server.NewMemoryClientStore()
	require.NoError(t, clients.Register(oauth2server.Client{
		ID:           "spa",
		RedirectURIs: []string{"https://app.example/callback"},
		PublicClient: true,
		Scopes:       []string{"openid"},
	}))
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-at-least-32-bytes-long"), time.Hour)
	require.NoError(t, err)
	return oauth2server.Options{
		Issuer:       "https://issuer.example",
		Clients:      clients,
		Codes:        oauth2server.NewMemoryCodeStore(),
		Signer:       signer,
		Authenticate: func(*http.Request) string { return "user" },
	}
}

type failingResponseWriter struct{ header http.Header }

func newFailingResponseWriter() *failingResponseWriter {
	return &failingResponseWriter{header: make(http.Header)}
}

func (w *failingResponseWriter) Header() http.Header { return w.header }

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("response write failed")
}

func (w *failingResponseWriter) WriteHeader(int) {}

func TestServer_RejectsBadPKCE(t *testing.T) {
	t.Parallel()

	clients := oauth2server.NewMemoryClientStore()
	require.NoError(t, clients.Register(oauth2server.Client{ID: "c", RedirectURIs: []string{"https://app.example/cb"}, PublicClient: true}))
	codes := oauth2server.NewMemoryCodeStore()
	require.NoError(t, codes.Save(context.Background(), oauth2server.Code{
		Value: "code-1",
		Req: oauth2server.AuthRequest{
			ClientID:            "c",
			UserID:              "u",
			RedirectURI:         "https://app.example/cb",
			CodeChallenge:       "challenge-x",
			CodeChallengeMethod: "S256",
			ExpiresAt:           time.Now().Add(time.Minute),
		},
	}))
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-at-least-32-bytes-long"), time.Hour)
	require.NoError(t, err)
	srv, err := oauth2server.New(oauth2server.Options{
		Issuer:       "iss",
		Clients:      clients,
		Codes:        codes,
		Signer:       signer,
		Authenticate: func(_ *http.Request) string { return "u" },
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", "code-1")
	form.Set("redirect_uri", "https://app.example/cb")
	form.Set("client_id", "c")
	form.Set("code_verifier", "wrong")
	resp, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

// newAuthorizeServer returns a test server whose single public client "spa"
// has exactly the given registered redirect URIs.
func newAuthorizeServer(t *testing.T, registered []string) *httptest.Server {
	t.Helper()
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-at-least-32-bytes-long"), time.Hour)
	require.NoError(t, err)
	srv, err := oauth2server.New(oauth2server.Options{
		Issuer:       "https://issuer.test",
		Clients:      staticClientStore{client: oauth2server.Client{ID: "spa", RedirectURIs: registered, PublicClient: true}},
		Codes:        oauth2server.NewMemoryCodeStore(),
		Signer:       signer,
		Clock:        clockwork.NewFakeClock(),
		Authenticate: func(_ *http.Request) string { return "user-1" },
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

type staticClientStore struct{ client oauth2server.Client }

func (s staticClientStore) Get(context.Context, string) (oauth2server.Client, error) {
	return s.client, nil
}

// authorize issues a non-following GET /authorize with valid S256 PKCE
// challenge and returns the status code and Location header. An empty
// redirectURI or state omits that query parameter entirely.
func authorize(t *testing.T, ts *httptest.Server, redirectURI, state string) (int, string) {
	t.Helper()
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "spa")
	verifier := "authorize-helper-verifier-is-at-least-43-characters"
	sum := sha256.Sum256([]byte(verifier))
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	if redirectURI != "" {
		q.Set("redirect_uri", redirectURI)
	}
	if state != "" {
		q.Set("state", state)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/authorize?"+q.Encode(), http.NoBody)
	require.NoError(t, err)
	noFollow := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode, resp.Header.Get("Location")
}

// TestServer_AuthorizeRedirectAllowList pins the invariant behind the
// open-redirect suppression in handleAuthorize: redirects match a registered
// URI exactly except for an RFC 8252 public-loopback port.
func TestServer_AuthorizeRedirectAllowList(t *testing.T) {
	t.Parallel()

	const cb = "https://app.example/callback"
	cases := []struct {
		name         string
		registered   []string
		redirectURI  string
		state        string
		wantStatus   int
		wantRedirect string
	}{
		{name: "exact registered entry", registered: []string{cb, "https://app.example/other"}, redirectURI: "https://app.example/other", state: "xyz", wantStatus: http.StatusFound, wantRedirect: "https://app.example/other"},
		{name: "hostile state stays inside the query", registered: []string{cb}, redirectURI: cb, state: "x#y&z=1//attacker.example", wantStatus: http.StatusFound, wantRedirect: cb},
		{name: "private-use native redirect", registered: []string{"com.example.app:/oauth2redirect/provider"}, redirectURI: "com.example.app:/oauth2redirect/provider", wantStatus: http.StatusFound, wantRedirect: "com.example.app:/oauth2redirect/provider"},
		{name: "loopback runtime port", registered: []string{"http://127.0.0.1/oauth2redirect/provider"}, redirectURI: "http://127.0.0.1:51004/oauth2redirect/provider", wantStatus: http.StatusFound, wantRedirect: "http://127.0.0.1:51004/oauth2redirect/provider"},
		// negative: anything that is not an exact registered entry is refused
		{name: "unregistered host", registered: []string{cb}, redirectURI: "https://attacker.example/callback", wantStatus: http.StatusBadRequest},
		{name: "registered prefix with extra path", registered: []string{cb}, redirectURI: cb + "/../evil", wantStatus: http.StatusBadRequest},
		{name: "registered entry with extra query", registered: []string{cb}, redirectURI: cb + "?next=http://attacker.example", wantStatus: http.StatusBadRequest},
		// boundary: empty inputs fail closed
		{name: "missing redirect_uri", registered: []string{cb}, wantStatus: http.StatusBadRequest},
		{name: "client with no registered URIs", registered: nil, redirectURI: cb, wantStatus: http.StatusBadRequest},
		{name: "empty registered entry never matches a missing redirect_uri", registered: []string{""}, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, loc := authorize(t, newAuthorizeServer(t, tc.registered), tc.redirectURI, tc.state)
			require.Equal(t, tc.wantStatus, status)
			if tc.wantStatus != http.StatusFound {
				require.Empty(t, loc, "a refused authorize request must not redirect anywhere")
				return
			}
			got, err := url.Parse(loc)
			require.NoError(t, err)
			require.NotEmpty(t, got.Query().Get("code"))
			require.Equal(t, tc.state, got.Query().Get("state"))
			query := got.Query()
			query.Del("code")
			query.Del("state")
			got.RawQuery = query.Encode()
			require.Equal(t, tc.wantRedirect, got.String())
		})
	}
}
