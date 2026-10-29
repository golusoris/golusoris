// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/oauth2server"
)

const (
	authVerifier = "client-auth-verifier-with-at-least-forty-three-characters"
	authRedirect = "https://app.example/callback?keep=a%2Bb"
)

func authQuery() url.Values {
	sum := sha256.Sum256([]byte(authVerifier))
	return url.Values{
		"response_type": {"code"}, "client_id": {"spa"}, "redirect_uri": {"https://app.example/callback"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"state&with=reserved+characters"},
	}
}

func TestServer_AuthorizationResponseIssuer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, parameter, value, wantError string
		failingStore                      bool
	}{
		{name: "success"},
		{name: "no state", parameter: "state"},
		{name: "unsupported response", parameter: "response_type", value: "token", wantError: "unsupported_response_type"},
		{name: "missing challenge", parameter: "code_challenge", wantError: "invalid_request"},
		{name: "invalid challenge", parameter: "code_challenge", value: "short", wantError: "invalid_request"},
		{name: "invalid method", parameter: "code_challenge_method", value: "plain", wantError: "invalid_request"},
		{name: "invalid scope", parameter: "scope", value: "unregistered", wantError: "invalid_scope"},
		{name: "store failure", failingStore: true, wantError: "server_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			opts := validOptions(t)
			opts.Issuer = "https://issuer.example/tenant/a%2Fb"
			if test.failingStore {
				opts.Codes = failingCodeStore{}
			}
			server, err := oauth2server.New(opts)
			require.NoError(t, err)
			query := authQuery()
			if test.parameter != "" {
				query.Set(test.parameter, test.value)
			}
			recorder := httptest.NewRecorder()
			server.Routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/authorize?"+query.Encode(), nil))
			require.Equal(t, http.StatusFound, recorder.Code, recorder.Body.String())
			location, err := url.Parse(recorder.Header().Get("Location"))
			require.NoError(t, err)
			require.Equal(t, []string{opts.Issuer}, location.Query()["iss"])
			require.Equal(t, query.Get("state"), location.Query().Get("state"))
			require.Equal(t, test.wantError, location.Query().Get("error"))
			require.Equal(t, test.wantError == "", location.Query().Get("code") != "")
		})
	}
}

func TestServer_AuthorizeUnauthenticatedDoesNotRedirect(t *testing.T) {
	t.Parallel()
	opts := validOptions(t)
	opts.Authenticate = func(*http.Request) string { return "" }
	server, err := oauth2server.New(opts)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	server.Routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/authorize?"+authQuery().Encode(), nil))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Empty(t, recorder.Header().Get("Location"))
}

func newClientAuthHandler(t *testing.T, id, secret string, public bool) http.Handler {
	t.Helper()
	opts := validOptions(t)
	clients := oauth2server.NewMemoryClientStore()
	require.NoError(t, clients.Register(oauth2server.Client{ID: id, Secret: secret, PublicClient: public, RedirectURIs: []string{authRedirect}}))
	opts.Clients = clients
	opts.Clock = clockwork.NewFakeClock()
	sum := sha256.Sum256([]byte(authVerifier))
	require.NoError(t, opts.Codes.Save(t.Context(), oauth2server.Code{Value: "auth-code", Req: oauth2server.AuthRequest{
		ClientID: id, UserID: "user", RedirectURI: authRedirect,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), CodeChallengeMethod: "S256", ExpiresAt: opts.Clock.Now().Add(time.Minute),
	}}))
	server, err := oauth2server.New(opts)
	require.NoError(t, err)
	return server.Routes()
}

func tokenAuthRequest(header string, credentials url.Values) *http.Request {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"auth-code"}, "redirect_uri": {authRedirect}, "code_verifier": {authVerifier}}
	maps.Copy(form, credentials)
	request := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if header != "" {
		request.Header.Set("Authorization", header)
	}
	return request
}

func basicHeader(id, secret string) string {
	return rawBasicHeader(url.QueryEscape(id) + ":" + url.QueryEscape(secret))
}

func rawBasicHeader(credentials string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
}

func TestServer_ClientAuthenticationSuccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, id, secret, header string
		form                     url.Values
		public                   bool
	}{
		{name: "basic", id: "backend", secret: "secret", header: basicHeader("backend", "secret")},
		{name: "case insensitive scheme", id: "backend", secret: "secret", header: "basic " + strings.TrimPrefix(basicHeader("backend", "secret"), "Basic ")},
		{name: "encoded colon percent plus space unicode", id: "client:%+ £", secret: "secret:%+ €", header: basicHeader("client:%+ £", "secret:%+ €")},
		{name: "decode exactly once", id: "client%3A", secret: "secret%25", header: basicHeader("client%3A", "secret%25")},
		{name: "post", id: "backend", secret: "secret", form: url.Values{"client_id": {"backend"}, "client_secret": {"secret"}}},
		{name: "public none", id: "spa", public: true, form: url.Values{"client_id": {"spa"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			newClientAuthHandler(t, test.id, test.secret, test.public).ServeHTTP(recorder, tokenAuthRequest(test.header, test.form))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var body struct {
				AccessToken string `json:"access_token"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.NotEmpty(t, body.AccessToken)
			require.Empty(t, recorder.Header().Get("WWW-Authenticate"))
		})
	}
}

func TestServer_ClientAuthenticationRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, header string
		form         url.Values
		mixed        bool
	}{
		{name: "wrong secret", header: basicHeader("backend", "wrong")},
		{name: "unknown client", header: basicHeader("unknown", "secret")},
		{name: "empty secret", header: basicHeader("backend", "")},
		{name: "empty id", header: basicHeader("", "secret")},
		{name: "malformed base64", header: "Basic !!!"},
		{name: "missing basic value", header: "Basic"},
		{name: "empty basic value", header: "Basic "},
		{name: "missing colon", header: rawBasicHeader("backend")},
		{name: "malformed id escape", header: rawBasicHeader("back%ZZ:secret")},
		{name: "malformed secret escape", header: rawBasicHeader("backend:sec%")},
		{name: "basic and post", header: basicHeader("backend", "secret"), form: url.Values{"client_id": {"backend"}, "client_secret": {"secret"}}, mixed: true},
		{name: "basic and form id", header: basicHeader("backend", "secret"), form: url.Values{"client_id": {"backend"}}, mixed: true},
		{name: "basic and form secret", header: basicHeader("backend", "secret"), form: url.Values{"client_secret": {"secret"}}, mixed: true},
		{name: "basic and empty form secret", header: basicHeader("backend", "secret"), form: url.Values{"client_secret": {""}}, mixed: true},
		{name: "malformed basic cannot fall back to post", header: "Basic !!!", form: url.Values{"client_id": {"backend"}, "client_secret": {"secret"}}, mixed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := newClientAuthHandler(t, "backend", "secret", false)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, tokenAuthRequest(test.header, test.form))
			wantStatus, wantError, wantChallenge := http.StatusUnauthorized, "invalid_client", "Basic"
			if test.mixed {
				wantStatus, wantError, wantChallenge = http.StatusBadRequest, "invalid_request", ""
			}
			require.Equal(t, wantStatus, recorder.Code, recorder.Body.String())
			var body struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.Equal(t, wantError, body.Error)
			require.Equal(t, wantChallenge, recorder.Header().Get("WWW-Authenticate"))
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
			retry := httptest.NewRecorder()
			handler.ServeHTTP(retry, tokenAuthRequest(basicHeader("backend", "secret"), nil))
			require.Equal(t, http.StatusOK, retry.Code, "failed authentication must not consume code: %s", retry.Body.String())
		})
	}
}
