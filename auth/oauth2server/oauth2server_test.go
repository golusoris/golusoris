// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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

func TestServer_AuthCodePKCEFlow(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	clients := oauth2server.NewMemoryClientStore()
	clients.Add(oauth2server.Client{
		ID:           "spa",
		RedirectURIs: []string{"http://localhost/callback"},
		PublicClient: true,
	})
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-long-enough"), time.Hour)
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
	q.Set("redirect_uri", "http://localhost/callback")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", "xyz")

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
	form.Set("redirect_uri", "http://localhost/callback")
	form.Set("client_id", "spa")
	form.Set("code_verifier", verifier)

	tokResp, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, tokResp.StatusCode)

	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	require.NoError(t, json.NewDecoder(tokResp.Body).Decode(&body))
	require.NoError(t, tokResp.Body.Close())
	require.NotEmpty(t, body.AccessToken)
	require.Equal(t, "Bearer", body.TokenType)

	// Reusing the code is rejected.
	tokResp2, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, tokResp2.StatusCode)
	require.NoError(t, tokResp2.Body.Close())
}

func TestServer_RejectsBadPKCE(t *testing.T) {
	t.Parallel()

	clients := oauth2server.NewMemoryClientStore()
	clients.Add(oauth2server.Client{ID: "c", RedirectURIs: []string{"http://x/cb"}, PublicClient: true})
	codes := oauth2server.NewMemoryCodeStore()
	require.NoError(t, codes.Save(context.Background(), oauth2server.Code{
		Value: "code-1",
		Req: oauth2server.AuthRequest{
			ClientID:            "c",
			UserID:              "u",
			RedirectURI:         "http://x/cb",
			CodeChallenge:       "challenge-x",
			CodeChallengeMethod: "S256",
			ExpiresAt:           time.Now().Add(time.Minute),
		},
	}))
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-long-enough"), time.Hour)
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
	form.Set("redirect_uri", "http://x/cb")
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
	clients := oauth2server.NewMemoryClientStore()
	clients.Add(oauth2server.Client{ID: "spa", RedirectURIs: registered, PublicClient: true})
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-long-enough"), time.Hour)
	require.NoError(t, err)
	srv, err := oauth2server.New(oauth2server.Options{
		Issuer:       "https://issuer.test",
		Clients:      clients,
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

// authorize issues a non-following GET /authorize with a valid plain PKCE
// challenge and returns the status code and Location header. An empty
// redirectURI or state omits that query parameter entirely.
func authorize(t *testing.T, ts *httptest.Server, redirectURI, state string) (int, string) {
	t.Helper()
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "spa")
	q.Set("code_challenge", "plain-challenge-that-is-long-enough-for-pkce-x")
	q.Set("code_challenge_method", "plain")
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
// open-redirect suppression in handleAuthorize: the Location header is only
// ever built from an exact entry of Client.RedirectURIs, and any other
// redirect_uri (or none at all) is refused with 400 and no Location header.
func TestServer_AuthorizeRedirectAllowList(t *testing.T) {
	t.Parallel()

	const cb = "http://localhost/callback"
	cases := []struct {
		name        string
		registered  []string
		redirectURI string
		state       string
		wantStatus  int
	}{
		// positive: exact matches redirect, and only to the registered entry
		{name: "exact registered entry", registered: []string{cb, "http://localhost/other"}, redirectURI: "http://localhost/other", state: "xyz", wantStatus: http.StatusFound},
		{name: "hostile state stays inside the query", registered: []string{cb}, redirectURI: cb, state: "x#y&z=1//attacker.example", wantStatus: http.StatusFound},
		// negative: anything that is not an exact registered entry is refused
		{name: "unregistered host", registered: []string{cb}, redirectURI: "http://attacker.example/callback", wantStatus: http.StatusBadRequest},
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
			require.Contains(t, tc.registered, got.Scheme+"://"+got.Host+got.Path, "Location must be a registered entry")
			require.NotEmpty(t, got.Query().Get("code"))
			require.Equal(t, tc.state, got.Query().Get("state"))
		})
	}
}

// errStore is returned by failingCodeStore to exercise the /authorize
// persistence-failure path.
var errStore = errors.New("code store unavailable")

// failingCodeStore is a CodeStore whose every operation fails.
type failingCodeStore struct{}

func (failingCodeStore) Save(_ context.Context, _ oauth2server.Code) error { return errStore }

func (failingCodeStore) Take(_ context.Context, _ string) (oauth2server.Code, error) {
	return oauth2server.Code{}, errStore
}

func TestServer_AuthorizeCodeStoreFailure(t *testing.T) {
	t.Parallel()

	clients := oauth2server.NewMemoryClientStore()
	clients.Add(oauth2server.Client{ID: "c", RedirectURIs: []string{"http://x/cb"}, PublicClient: true})
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-long-enough"), time.Hour)
	require.NoError(t, err)
	srv, err := oauth2server.New(oauth2server.Options{
		Issuer:       "iss",
		Clients:      clients,
		Codes:        failingCodeStore{},
		Signer:       signer,
		Authenticate: func(_ *http.Request) string { return "u" },
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "c")
	q.Set("redirect_uri", "http://x/cb")
	q.Set("code_challenge", "challenge-x")
	q.Set("code_challenge_method", "S256")

	noFollow := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(ts.URL + "/authorize?" + q.Encode())
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}
