// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	require.NoError(t, clients.Register(oauth2server.Client{ID: "c", RedirectURIs: []string{"https://app.example/cb"}, PublicClient: true}))
	signer, err := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-at-least-32-bytes-long"), time.Hour)
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
	q.Set("redirect_uri", "https://app.example/cb")
	verifier := "store-failure-verifier-with-at-least-43-characters"
	sum := sha256.Sum256([]byte(verifier))
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")

	noFollow := &http.Client{
		Transport:     ts.Client().Transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noFollow.Get(ts.URL + "/authorize?" + q.Encode())
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}
