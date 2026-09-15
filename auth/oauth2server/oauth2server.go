// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package oauth2server is a minimal OAuth 2.1 / OIDC issuer implementing
// the authorization-code-with-PKCE grant. It does not aim to be a fully
// spec-conformant IdP — it covers the common case of "be the IdP for my
// own first-party apps".
//
// Not implemented: implicit grant (deprecated by 2.1), password grant
// (deprecated), client credentials, device code, dynamic client
// registration, refresh tokens.
//
// Mount [Server.Routes] under your router; wire [Options.Authenticate]
// to your session/login handler so /authorize can identify the user.
package oauth2server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/auth/jwt"
)

// maxFormBytes caps the size of x-www-form-urlencoded bodies on the
// /token endpoint. OAuth requests are tiny — 8 KiB is generous.
const maxFormBytes = 8 << 10

// Client is a registered OAuth client.
type Client struct {
	ID           string
	RedirectURIs []string
	// PublicClient: when true, no client_secret check is performed
	// (browser SPA / mobile app flows).
	PublicClient bool
	Secret       string
	Scopes       []string
}

// AuthRequest captures the user-consented authorization-code grant.
type AuthRequest struct {
	ClientID            string
	UserID              string
	Scope               string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string // "S256" or "plain"
	IssuedAt            time.Time
	ExpiresAt           time.Time
}

// Code is a short-lived authorization code persisted by [CodeStore].
type Code struct {
	Value string
	Req   AuthRequest
}

// CodeStore persists authorization codes (single-use, ~60s TTL).
type CodeStore interface {
	Save(ctx context.Context, c Code) error
	Take(ctx context.Context, value string) (Code, error) // delete-on-read
}

// ClientStore returns clients by ID.
type ClientStore interface {
	Get(ctx context.Context, id string) (Client, error)
}

// Options configure the server.
type Options struct {
	Issuer    string
	Clients   ClientStore
	Codes     CodeStore
	Signer    *jwt.Signer
	Clock     clockwork.Clock
	AccessTTL time.Duration // default 1h
	CodeTTL   time.Duration // default 60s
	// Authenticate must return the userID for the current request,
	// or empty string when the user is unauthenticated.
	Authenticate func(r *http.Request) (userID string)
}

// Server implements the OAuth2 endpoints.
type Server struct{ opts Options }

// New constructs a Server. Returns an error on invalid configuration.
func New(opts Options) (*Server, error) {
	if opts.Issuer == "" || opts.Clients == nil || opts.Codes == nil || opts.Signer == nil || opts.Authenticate == nil {
		return nil, errors.New("oauth2server: Issuer, Clients, Codes, Signer, Authenticate required")
	}
	if opts.Clock == nil {
		opts.Clock = clockwork.NewRealClock()
	}
	if opts.AccessTTL == 0 {
		opts.AccessTTL = time.Hour
	}
	if opts.CodeTTL == 0 {
		opts.CodeTTL = 60 * time.Second
	}
	return &Server{opts: opts}, nil
}

// Routes returns an http.Handler exposing /authorize and /token.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)
	return mux
}

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("response_type") != "code" {
		http.Error(w, "only response_type=code supported", http.StatusBadRequest)
		return
	}
	clientID := q.Get("client_id")
	client, err := s.opts.Clients.Get(r.Context(), clientID)
	if err != nil {
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	}
	// Resolve the user-supplied redirect_uri to an exact entry in the
	// client's registered list. From here on we only ever use `redirect`,
	// the trusted value copied out of config — never the raw query param.
	// This gives CodeQL a clear allow-list sanitizer for the open-redirect
	// taint flow into http.Redirect below.
	redirect := pickRegistered(client.RedirectURIs, q.Get("redirect_uri")) // nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- exact-match allow-list of registered URIs
	if redirect == "" {
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	}
	codeChallenge := q.Get("code_challenge")
	method := q.Get("code_challenge_method")
	if codeChallenge == "" {
		http.Error(w, "PKCE required: code_challenge missing", http.StatusBadRequest)
		return
	}
	if method == "" {
		method = "plain"
	}
	if method != "S256" && method != "plain" {
		http.Error(w, "unsupported code_challenge_method", http.StatusBadRequest)
		return
	}

	userID := s.opts.Authenticate(r)
	if userID == "" {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}

	code, err := s.issueCode(r.Context(), AuthRequest{
		ClientID:            clientID,
		UserID:              userID,
		Scope:               q.Get("scope"),
		RedirectURI:         redirect,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: method,
	})
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	redirectWithCode(w, r, redirect, code, q.Get("state"))
}

// issueCode mints a single-use authorization code for req, stamps its
// validity window, and persists it.
func (s *Server) issueCode(ctx context.Context, req AuthRequest) (string, error) {
	code, err := randomB64(24)
	if err != nil {
		return "", err
	}
	now := s.opts.Clock.Now()
	req.IssuedAt = now
	req.ExpiresAt = now.Add(s.opts.CodeTTL)
	if saveErr := s.opts.Codes.Save(ctx, Code{Value: code, Req: req}); saveErr != nil {
		return "", fmt.Errorf("oauth2server: save code: %w", saveErr)
	}
	return code, nil
}

// redirectWithCode sends the client back to the registered redirect URI
// with the code (and state, when supplied) appended.
func redirectWithCode(w http.ResponseWriter, r *http.Request, redirect, code, state string) {
	u, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	v := u.Query()
	v.Set("code", code)
	if state != "" {
		v.Set("state", state)
	}
	u.RawQuery = v.Encode()
	// Semgrep's taint-mode open-redirect rule reports at this sink, not at the
	// pickRegistered allow-list above; TestServer_AuthorizeRedirectAllowList pins the invariant.
	http.Redirect(w, r, u.String(), http.StatusFound) // nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- u is built from the exact-match allow-listed entry of client.RedirectURIs (pickRegistered), never the raw redirect_uri
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		writeTokenErr(w, http.StatusBadRequest, "invalid_request", "form parse failed")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		writeTokenErr(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code")
		return
	}

	code, terr := s.resolveCode(r)
	if terr != nil {
		writeTokenErr(w, terr.status, terr.code, terr.desc)
		return
	}

	client, terr := s.authenticateClient(r)
	if terr != nil {
		writeTokenErr(w, terr.status, terr.code, terr.desc)
		return
	}

	verifier := r.PostForm.Get("code_verifier")
	if !verifyPKCE(code.Req.CodeChallenge, code.Req.CodeChallengeMethod, verifier) {
		writeTokenErr(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	resp, err := s.mintAccessToken(client.ID, code.Req)
	if err != nil {
		writeTokenErr(w, http.StatusInternalServerError, "server_error", "sign")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

// tokenErrInfo carries the status/error/description triple for a rejected
// /token request, ready to hand to writeTokenErr.
type tokenErrInfo struct {
	status int
	code   string
	desc   string
}

// resolveCode looks up the authorization code named by the request's "code"
// form value, and confirms it has not expired and was issued for the same
// redirect_uri and client_id supplied on this request. The returned
// *tokenErrInfo, when non-nil, is the exact error the caller should write.
func (s *Server) resolveCode(r *http.Request) (Code, *tokenErrInfo) {
	code, err := s.opts.Codes.Take(r.Context(), r.PostForm.Get("code"))
	if err != nil {
		return Code{}, &tokenErrInfo{status: http.StatusBadRequest, code: "invalid_grant", desc: "code not found"}
	}
	if s.opts.Clock.Now().After(code.Req.ExpiresAt) {
		return Code{}, &tokenErrInfo{status: http.StatusBadRequest, code: "invalid_grant", desc: "code expired"}
	}
	if r.PostForm.Get("redirect_uri") != code.Req.RedirectURI {
		return Code{}, &tokenErrInfo{status: http.StatusBadRequest, code: "invalid_grant", desc: "redirect_uri mismatch"}
	}
	if r.PostForm.Get("client_id") != code.Req.ClientID {
		return Code{}, &tokenErrInfo{status: http.StatusBadRequest, code: "invalid_client", desc: "client mismatch"}
	}
	return code, nil
}

// authenticateClient looks up the client named by the request's "client_id"
// form value and, for confidential clients, verifies the client_secret. The
// returned *tokenErrInfo, when non-nil, is the exact error the caller should
// write.
func (s *Server) authenticateClient(r *http.Request) (Client, *tokenErrInfo) {
	clientID := r.PostForm.Get("client_id")
	client, err := s.opts.Clients.Get(r.Context(), clientID)
	if err != nil {
		return Client{}, &tokenErrInfo{status: http.StatusBadRequest, code: "invalid_client", desc: "unknown client"}
	}
	if !client.PublicClient {
		secret := r.PostForm.Get("client_secret")
		if subtle.ConstantTimeCompare([]byte(secret), []byte(client.Secret)) != 1 {
			return Client{}, &tokenErrInfo{status: http.StatusUnauthorized, code: "invalid_client", desc: "bad secret"}
		}
	}
	return client, nil
}

// mintAccessToken signs a bearer JWT for the consented authorization request.
func (s *Server) mintAccessToken(clientID string, req AuthRequest) (tokenResponse, error) {
	jti, err := randomB64(16)
	if err != nil {
		return tokenResponse{}, err
	}
	now := s.opts.Clock.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    s.opts.Issuer,
		Subject:   req.UserID,
		Audience:  []string{clientID},
		IssuedAt:  gojwt.NewNumericDate(now),
		ExpiresAt: gojwt.NewNumericDate(now.Add(s.opts.AccessTTL)),
		ID:        jti,
	}
	tok, err := s.opts.Signer.Sign(claims)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("oauth2server: sign: %w", err)
	}
	return tokenResponse{
		AccessToken: tok,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.opts.AccessTTL.Seconds()),
		Scope:       req.Scope,
	}, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope,omitempty"`
}

type tokenErr struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func writeTokenErr(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, tokenErr{Error: code, ErrorDescription: desc})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil { //nolint:gosec // G117: access_token is intentionally marshaled in OAuth response body // #nosec G117
		return // status + headers already sent; nothing more to report to the client
	}
}

func verifyPKCE(challenge, method, verifier string) bool {
	if verifier == "" {
		return false
	}
	switch method {
	case "plain":
		return subtle.ConstantTimeCompare([]byte(challenge), []byte(verifier)) == 1
	case "S256":
		sum := sha256.Sum256([]byte(verifier))
		got := base64.RawURLEncoding.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(challenge), []byte(got)) == 1
	}
	return false
}

func randomB64(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth2server: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// pickRegistered returns the registered entry that exactly matches `candidate`,
// or "" if no entry matches. Used as an allow-list sanitizer for redirect_uri
// so the value passed to http.Redirect is always a trusted config string.
func pickRegistered(registered []string, candidate string) string {
	for _, s := range registered {
		if s == candidate {
			return s
		}
	}
	return ""
}

// MemoryClientStore is an in-process ClientStore for tests / single-replica use.
type MemoryClientStore struct {
	mu sync.Mutex
	m  map[string]Client
}

// NewMemoryClientStore returns an initialised store.
func NewMemoryClientStore() *MemoryClientStore { return &MemoryClientStore{m: map[string]Client{}} }

// Add registers a client.
func (s *MemoryClientStore) Add(c Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[c.ID] = c
}

// Get returns the client by ID.
func (s *MemoryClientStore) Get(_ context.Context, id string) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[id]
	if !ok {
		return Client{}, fmt.Errorf("oauth2server: client %q not found", id)
	}
	return c, nil
}

// MemoryCodeStore is a single-use in-memory CodeStore.
type MemoryCodeStore struct {
	mu sync.Mutex
	m  map[string]Code
}

// NewMemoryCodeStore returns an initialised store.
func NewMemoryCodeStore() *MemoryCodeStore { return &MemoryCodeStore{m: map[string]Code{}} }

// Save persists a code.
func (s *MemoryCodeStore) Save(_ context.Context, c Code) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[c.Value] = c
	return nil
}

// Take returns and deletes the code (single-use).
func (s *MemoryCodeStore) Take(_ context.Context, value string) (Code, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[value]
	if !ok {
		return Code{}, errors.New("oauth2server: code not found")
	}
	delete(s.m, value)
	return c, nil
}
