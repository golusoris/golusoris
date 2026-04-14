package oauth2server_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

func TestServer_AuthCodePKCEFlow(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	clients := oauth2server.NewMemoryClientStore()
	clients.Add(oauth2server.Client{
		ID:           "spa",
		RedirectURIs: []string{"http://localhost/callback"},
		PublicClient: true,
	})
	signer := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-long-enough"), time.Hour)

	srv := oauth2server.New(oauth2server.Options{
		Issuer:       "https://issuer.test",
		Clients:      clients,
		Codes:        oauth2server.NewMemoryCodeStore(),
		Signer:       signer,
		Clock:        clk,
		Authenticate: func(_ *http.Request) string { return "user-1" },
	})

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
	signer := jwt.NewHMACSigner(jwt.HS256, []byte("topsecret-and-long-enough"), time.Hour)
	srv := oauth2server.New(oauth2server.Options{
		Issuer:       "iss",
		Clients:      clients,
		Codes:        codes,
		Signer:       signer,
		Authenticate: func(_ *http.Request) string { return "u" },
	})
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
