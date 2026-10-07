// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package oauth2server is a minimal OAuth authorization server implementing
// the authorization-code-with-PKCE grant for first-party applications. It is
// not an OpenID Connect issuer: it does not issue ID tokens or expose OIDC
// discovery, UserInfo, or nonce handling.
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
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/auth/jwt"
	"github.com/golusoris/golusoris/core/validate"
)

// maxFormBytes caps the size of x-www-form-urlencoded bodies on the
// /token endpoint. OAuth requests are tiny — 8 KiB is generous.
const (
	maxFormBytes      = 8 << 10
	maxScopeBytes     = 1 << 10
	maxRequestedScope = 64
)

var pkceValuePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

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
	CodeChallengeMethod string // "S256"
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
	// Logger receives response-encoding failures after headers are committed.
	Logger *slog.Logger
}

// Server implements the OAuth2 endpoints.
type Server struct{ opts Options }

// New constructs a Server. Returns an error on invalid configuration.
func New(opts Options) (*Server, error) {
	if err := validateServerDependencies(opts); err != nil {
		return nil, err
	}
	if validate.IsNil(opts.Clock) {
		opts.Clock = clockwork.NewRealClock()
	}
	if err := normalizeTTLs(&opts); err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Server{opts: opts}, nil
}

func validateServerDependencies(opts Options) error {
	if opts.Issuer == "" || validate.IsNil(opts.Clients) || validate.IsNil(opts.Codes) || opts.Signer == nil || opts.Authenticate == nil {
		return errors.New("oauth2server: Issuer, Clients, Codes, Signer, Authenticate required")
	}
	return nil
}

func normalizeTTLs(opts *Options) error {
	if opts.AccessTTL == 0 {
		opts.AccessTTL = time.Hour
	}
	if opts.AccessTTL < time.Second {
		return errors.New("oauth2server: AccessTTL must be at least one second")
	}
	if opts.CodeTTL == 0 {
		opts.CodeTTL = 60 * time.Second
	}
	if opts.CodeTTL < time.Nanosecond {
		return errors.New("oauth2server: CodeTTL must be positive")
	}
	return nil
}

// Routes returns an http.Handler exposing /authorize and /token.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", s.handleAuthorize)
	mux.HandleFunc("/token", s.handleToken)
	return mux
}

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	if q.Get("response_type") != "code" {
		http.Error(w, "only response_type=code supported", http.StatusBadRequest)
		return
	}
	clientID := q.Get("client_id")
	client, err := s.registeredClient(r.Context(), clientID)
	if err != nil {
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	}
	// Resolve redirect_uri against the client's registered list. Only RFC 8252
	// loopback redirects may vary, and then only by port.
	redirect := pickRegistered(client.RedirectURIs, q.Get("redirect_uri"), client.PublicClient) // nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- registered allow-list; public loopback redirects vary only by port
	if redirect == "" {
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	}
	codeChallenge, method, pkceError := authorizePKCE(q)
	if pkceError != "" {
		http.Error(w, pkceError, http.StatusBadRequest)
		return
	}
	scope, ok := authorizedScopes(q.Get("scope"), client.Scopes)
	if !ok {
		http.Error(w, "invalid_scope", http.StatusBadRequest)
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
		Scope:               scope,
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

func authorizePKCE(query url.Values) (challenge, method, errorMessage string) {
	challenge = query.Get("code_challenge")
	if challenge == "" {
		return "", "", "PKCE required: code_challenge missing"
	}
	method = query.Get("code_challenge_method")
	if method != "S256" {
		return "", "", "code_challenge_method must be S256"
	}
	if !validPKCEValue(challenge) {
		return "", "", "invalid code_challenge"
	}
	return challenge, method, ""
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
	http.Redirect(w, r, u.String(), http.StatusFound) // #nosec G710 -- pickRegistered uses registered scheme/host/path/query; only a validated public-loopback port can vary // nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- same registered allow-list invariant
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	setTokenCacheHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeTokenErr(w, r, http.StatusMethodNotAllowed, "invalid_request", "method must be POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		s.writeTokenErr(w, r, http.StatusBadRequest, "invalid_request", "form parse failed")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		s.writeTokenErr(w, r, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code")
		return
	}

	client, terr := s.authenticateClient(r)
	if terr != nil {
		s.writeTokenErr(w, r, terr.status, terr.code, terr.desc)
		return
	}

	code, terr := s.resolveCode(r)
	if terr != nil {
		s.writeTokenErr(w, r, terr.status, terr.code, terr.desc)
		return
	}
	scope, ok := authorizedScopes(code.Req.Scope, client.Scopes)
	if !ok {
		s.writeTokenErr(w, r, http.StatusBadRequest, "invalid_scope", "scope no longer allowed")
		return
	}
	code.Req.Scope = scope

	verifier := r.PostForm.Get("code_verifier")
	if !verifyPKCE(code.Req.CodeChallenge, code.Req.CodeChallengeMethod, verifier) {
		s.writeTokenErr(w, r, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	resp, err := s.mintAccessToken(client.ID, code.Req)
	if err != nil {
		s.writeTokenErr(w, r, http.StatusInternalServerError, "server_error", "sign")
		return
	}
	s.writeJSON(w, r, http.StatusOK, resp)
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
	if !s.opts.Clock.Now().Before(code.Req.ExpiresAt) {
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
	client, err := s.registeredClient(r.Context(), clientID)
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

func (s *Server) registeredClient(ctx context.Context, id string) (Client, error) {
	client, err := s.opts.Clients.Get(ctx, id)
	if err != nil {
		return Client{}, fmt.Errorf("oauth2server: get client: %w", err)
	}
	if client.ID != id {
		return Client{}, errors.New("oauth2server: client store returned mismatched ID")
	}
	if err := validateClient(client); err != nil {
		return Client{}, err
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
	claims := accessTokenClaims{
		Issuer:    s.opts.Issuer,
		Subject:   req.UserID,
		Audience:  []string{clientID},
		IssuedAt:  gojwt.NewNumericDate(now),
		ExpiresAt: gojwt.NewNumericDate(now.Add(s.opts.AccessTTL)),
		ID:        jti,
		Scope:     req.Scope,
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

type accessTokenClaims struct {
	jwt.RegisteredClaims
	Scope string `json:"scope,omitempty"`
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

func (s *Server) writeTokenErr(w http.ResponseWriter, r *http.Request, status int, code, desc string) {
	setTokenCacheHeaders(w)
	s.writeJSON(w, r, status, tokenErr{Error: code, ErrorDescription: desc})
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil { //nolint:gosec // G117: access_token is intentionally marshaled in OAuth response body // #nosec G117
		s.opts.Logger.ErrorContext(r.Context(), "oauth2server: encode response failed", slog.Any("error", err))
	}
}

func setTokenCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func verifyPKCE(challenge, method, verifier string) bool {
	if method != "S256" || !validPKCEValue(challenge) || !validPKCEValue(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(challenge), []byte(got)) == 1
}

func validPKCEValue(value string) bool {
	return pkceValuePattern.MatchString(value)
}

func randomB64(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth2server: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

func authorizedScopes(requested string, allowed []string) (string, bool) {
	if len(requested) > maxScopeBytes {
		return "", false
	}
	if requested == "" {
		return "", true
	}
	requestedScopes := strings.Split(requested, " ")
	if len(requestedScopes) > maxRequestedScope {
		return "", false
	}
	seen := make(map[string]struct{}, len(requestedScopes))
	normalized := make([]string, 0, len(requestedScopes))
	for _, scope := range requestedScopes {
		if !validScopeToken(scope) || !contains(allowed, scope) {
			return "", false
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	return strings.Join(normalized, " "), true
}

// pickRegistered returns an allowed redirect or "". Redirects match exactly
// except that RFC 8252 public loopback redirects may choose a runtime port.
func pickRegistered(registered []string, candidate string, publicClient bool) string {
	for _, s := range registered {
		if s == candidate {
			return s
		}
	}
	if !publicClient {
		return ""
	}
	candidateURL, err := url.Parse(candidate)
	if err != nil || !isLoopbackRedirect(candidateURL) {
		return ""
	}
	port, ok := redirectPort(candidateURL.Port())
	if !ok {
		return ""
	}
	for _, raw := range registered {
		registeredURL, parseErr := url.Parse(raw)
		if parseErr == nil && loopbackRedirectsMatch(registeredURL, candidateURL) {
			return registeredRedirectWithPort(registeredURL, port)
		}
	}
	return ""
}

// Only the parsed port number crosses from the request; 0 keeps the registered host.
func registeredRedirectWithPort(registered *url.URL, port int) string {
	allowed := *registered
	host := registered.Hostname()
	if port != 0 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	allowed.Host = host
	return allowed.String()
}

func loopbackRedirectsMatch(registered, candidate *url.URL) bool {
	return isLoopbackRedirect(registered) && isLoopbackRedirect(candidate) &&
		registered.Hostname() == candidate.Hostname() &&
		registered.EscapedPath() == candidate.EscapedPath() &&
		registered.RawQuery == candidate.RawQuery &&
		registered.ForceQuery == candidate.ForceQuery
}

func isLoopbackRedirect(uri *url.URL) bool {
	if uri == nil || uri.Scheme != "http" || uri.User != nil || uri.Fragment != "" || uri.Opaque != "" {
		return false
	}
	ip := net.ParseIP(uri.Hostname())
	_, portOK := redirectPort(uri.Port())
	return ip != nil && ip.IsLoopback() && portOK
}

// redirectPort parses an optional URI port; an absent port yields 0.
func redirectPort(port string) (int, bool) {
	if port == "" {
		return 0, true
	}
	number, err := strconv.Atoi(port)
	if err != nil || number <= 0 || number > 65535 {
		return 0, false
	}
	return number, true
}

// MemoryClientStore is an in-process ClientStore for tests / single-replica use.
type MemoryClientStore struct {
	mu sync.Mutex
	m  map[string]Client
}

// NewMemoryClientStore returns an initialised store.
func NewMemoryClientStore() *MemoryClientStore { return &MemoryClientStore{m: map[string]Client{}} }

// Add preserves the legacy registration API. Invalid clients are not stored;
// use Register when the caller must receive the validation error.
func (s *MemoryClientStore) Add(c Client) {
	if err := s.Register(c); err != nil {
		return
	}
}

// Register validates, copies, and registers a client.
func (s *MemoryClientStore) Register(c Client) error {
	if err := validateClient(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[c.ID] = cloneClient(c)
	return nil
}

// Get returns the client by ID.
func (s *MemoryClientStore) Get(_ context.Context, id string) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[id]
	if !ok {
		return Client{}, fmt.Errorf("oauth2server: client %q not found", id)
	}
	return cloneClient(c), nil
}

func cloneClient(client Client) Client {
	client.RedirectURIs = append([]string(nil), client.RedirectURIs...)
	client.Scopes = append([]string(nil), client.Scopes...)
	return client
}

func validateClient(client Client) error {
	if strings.TrimSpace(client.ID) == "" {
		return errors.New("oauth2server: client ID required")
	}
	if err := validateRedirectURIs(client.RedirectURIs, client.PublicClient); err != nil {
		return err
	}
	if err := validateClientScopes(client.Scopes); err != nil {
		return err
	}
	if !client.PublicClient && client.Secret == "" {
		return errors.New("oauth2server: confidential client secret required")
	}
	return nil
}

func validateRedirectURIs(registered []string, publicClient bool) error {
	if len(registered) == 0 {
		return errors.New("oauth2server: client requires a redirect URI")
	}
	redirects := make(map[string]struct{}, len(registered))
	for _, raw := range registered {
		if err := validateRedirectURI(raw, publicClient); err != nil {
			return err
		}
		if _, exists := redirects[raw]; exists {
			return fmt.Errorf("oauth2server: duplicate client redirect URI %q", raw)
		}
		redirects[raw] = struct{}{}
	}
	return nil
}

func validateRedirectURI(raw string, publicClient bool) error {
	u, err := url.Parse(raw)
	if err != nil || !validRedirectBase(u) {
		return fmt.Errorf("oauth2server: invalid client redirect URI %q", raw)
	}
	if isWebRedirect(u, publicClient) || (publicClient && isPrivateUseRedirect(raw, u)) {
		return nil
	}
	return fmt.Errorf("oauth2server: invalid client redirect URI %q", raw)
}

func validRedirectBase(uri *url.URL) bool {
	return uri != nil && uri.Scheme != "" && uri.User == nil && uri.Fragment == "" && uri.Opaque == ""
}

func isWebRedirect(uri *url.URL, publicClient bool) bool {
	if uri.Scheme == "https" && uri.Host != "" {
		return true
	}
	return publicClient && isLoopbackRedirect(uri)
}

func isPrivateUseRedirect(raw string, uri *url.URL) bool {
	privatePrefix := uri.Scheme + ":/"
	return strings.Contains(uri.Scheme, ".") && uri.Host == "" && uri.Path != "" &&
		strings.HasPrefix(raw, privatePrefix) && !strings.HasPrefix(raw, privatePrefix+"/")
}

func validateClientScopes(registered []string) error {
	scopes := make(map[string]struct{}, len(registered))
	for _, scope := range registered {
		if err := validateClientScope(scope); err != nil {
			return err
		}
		if _, exists := scopes[scope]; exists {
			return fmt.Errorf("oauth2server: duplicate client scope %q", scope)
		}
		scopes[scope] = struct{}{}
	}
	return nil
}

func validateClientScope(scope string) error {
	if len(scope) > maxScopeBytes || !validScopeToken(scope) {
		return fmt.Errorf("oauth2server: invalid client scope %q", scope)
	}
	return nil
}

func validScopeToken(scope string) bool {
	if scope == "" {
		return false
	}
	for i := range len(scope) {
		char := scope[i]
		if char != 0x21 && (char < 0x23 || char > 0x5b) && (char < 0x5d || char > 0x7e) {
			return false
		}
	}
	return true
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
