// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/jwt"
	"github.com/golusoris/golusoris/auth/oauth2server"
)

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
