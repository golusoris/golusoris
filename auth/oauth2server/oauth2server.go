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

// New constructs a Server. Panics on invalid configuration.
func New(opts Options) *Server {
	if opts.Issuer == "" || opts.Clients == nil || opts.Codes == nil || opts.Signer == nil || opts.Authenticate == nil {
		panic("oauth2server: Issuer, Clients, Codes, Signer, Authenticate required")
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
	return &Server{opts: opts}
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
	redirect := q.Get("redirect_uri")
	if !contains(client.RedirectURIs, redirect) {
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

	code, err := randomB64(24)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	now := s.opts.Clock.Now()
	saveErr := s.opts.Codes.Save(r.Context(), Code{
		Value: code,
		Req: AuthRequest{
			ClientID:            clientID,
			UserID:              userID,
			Scope:               q.Get("scope"),
			RedirectURI:         redirect,
			CodeChallenge:       codeChallenge,
			CodeChallengeMethod: method,
			IssuedAt:            now,
			ExpiresAt:           now.Add(s.opts.CodeTTL),
		},
	})
	if saveErr != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	u, parseErr := url.Parse(redirect)
	if parseErr != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	v := u.Query()
	v.Set("code", code)
	if state := q.Get("state"); state != "" {
		v.Set("state", state)
	}
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
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
	code, err := s.opts.Codes.Take(r.Context(), r.PostForm.Get("code"))
	if err != nil {
		writeTokenErr(w, http.StatusBadRequest, "invalid_grant", "code not found")
		return
	}
	if s.opts.Clock.Now().After(code.Req.ExpiresAt) {
		writeTokenErr(w, http.StatusBadRequest, "invalid_grant", "code expired")
		return
	}
	if r.PostForm.Get("redirect_uri") != code.Req.RedirectURI {
		writeTokenErr(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	clientID := r.PostForm.Get("client_id")
	if clientID != code.Req.ClientID {
		writeTokenErr(w, http.StatusBadRequest, "invalid_client", "client mismatch")
		return
	}
	client, err := s.opts.Clients.Get(r.Context(), clientID)
	if err != nil {
		writeTokenErr(w, http.StatusBadRequest, "invalid_client", "unknown client")
		return
	}
	if !client.PublicClient {
		secret := r.PostForm.Get("client_secret")
		if subtle.ConstantTimeCompare([]byte(secret), []byte(client.Secret)) != 1 {
			writeTokenErr(w, http.StatusUnauthorized, "invalid_client", "bad secret")
			return
		}
	}
	verifier := r.PostForm.Get("code_verifier")
	if !verifyPKCE(code.Req.CodeChallenge, code.Req.CodeChallengeMethod, verifier) {
		writeTokenErr(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	now := s.opts.Clock.Now()
	jti, _ := randomB64(16)
	claims := jwt.RegisteredClaims{
		Issuer:    s.opts.Issuer,
		Subject:   code.Req.UserID,
		Audience:  []string{client.ID},
		IssuedAt:  gojwt.NewNumericDate(now),
		ExpiresAt: gojwt.NewNumericDate(now.Add(s.opts.AccessTTL)),
		ID:        jti,
	}
	tok, err := s.opts.Signer.Sign(claims)
	if err != nil {
		writeTokenErr(w, http.StatusInternalServerError, "server_error", "sign")
		return
	}
	resp := tokenResponse{
		AccessToken: tok,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.opts.AccessTTL.Seconds()),
		Scope:       code.Req.Scope,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp) //nolint:gosec // G117: access_token is intentionally marshaled in OAuth response body // #nosec G117
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(tokenErr{Error: code, ErrorDescription: desc})
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
