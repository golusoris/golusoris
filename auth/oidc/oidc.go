// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package oidc provides an OIDC + OAuth 2.0 PKCE client as an fx
// module. It builds on [coreos/go-oidc/v3] and [golang.org/x/oauth2].
//
// Flow:
//  1. [Provider.AuthURL] → redirect the user to the IdP.
//  2. IdP calls back to your redirect URI with ?code=…&state=…
//  3. [Provider.Exchange] trades code for tokens and returns [TokenSet].
//  4. [Provider.UserInfo] fetches the OIDC UserInfo endpoint.
//
// PKCE (RFC 7636) is enabled by default — code_challenge is sent on
// every auth request. Store the verifier (returned by AuthURL) in the
// session before redirecting.
//
// Discovery uses an injected, timeout-bounded HTTP client.
// Config key prefix: auth.oidc.*
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"go.uber.org/fx"
	"golang.org/x/oauth2"

	"github.com/golusoris/golusoris/core/config"
	gerr "github.com/golusoris/golusoris/core/errors"
)

const defaultDiscoveryTimeout = 10 * time.Second

// Options configure the OIDC provider.
type Options struct {
	// IssuerURL is the OIDC issuer (e.g. "https://accounts.google.com").
	// Required.
	IssuerURL string `koanf:"issuer_url"`
	// ClientID is the OAuth2 client ID. Required.
	ClientID string `koanf:"client_id"`
	// ClientSecret is the OAuth2 client secret. Required for
	// confidential clients; leave empty for public clients.
	ClientSecret string `koanf:"client_secret"`
	// RedirectURL is the callback URL registered with the IdP. Required.
	RedirectURL string `koanf:"redirect_url"`
	// Scopes requested in addition to "openid" (default: email, profile).
	Scopes []string `koanf:"scopes"`
	// DiscoveryTimeout bounds OIDC discovery and the default HTTP client.
	DiscoveryTimeout time.Duration `koanf:"discovery_timeout"`
	// LazyDiscovery defers OIDC discovery until the first operation requiring it.
	LazyDiscovery bool `koanf:"lazy_discovery"`
	// HTTPClient performs discovery and subsequent OIDC requests. A copy gains
	// DiscoveryTimeout when its Timeout is not already positive.
	HTTPClient *http.Client `koanf:"-"`
}

func (o Options) withDefaults() Options {
	if len(o.Scopes) == 0 {
		o.Scopes = []string{gooidc.ScopeOpenID, "email", "profile"}
	} else {
		o.Scopes = append([]string(nil), o.Scopes...)
		if !slices.Contains(o.Scopes, gooidc.ScopeOpenID) {
			o.Scopes = append([]string{gooidc.ScopeOpenID}, o.Scopes...)
		}
	}
	if o.DiscoveryTimeout <= 0 {
		o.DiscoveryTimeout = defaultDiscoveryTimeout
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: o.DiscoveryTimeout}
	} else {
		client := *o.HTTPClient
		if client.Timeout <= 0 {
			client.Timeout = o.DiscoveryTimeout
		}
		o.HTTPClient = &client
	}
	return o
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := Options{}
	if err := cfg.Unmarshal("auth.oidc", &opts); err != nil {
		return Options{}, fmt.Errorf("auth/oidc: load options: %w", err)
	}
	return opts.withDefaults(), nil
}

// AuthRequest holds the authorization URL, the PKCE verifier, and the OIDC nonce.
// Store Verifier and Nonce in the session, pass back to [Provider.Complete].
type AuthRequest struct {
	URL      string
	Verifier string
	Nonce    string
}

// TokenSet holds the tokens returned after a successful code exchange.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	Expiry       time.Time
	// Subject is the subject identifier from the verified ID token.
	Subject string
	// Nonce is the nonce claim from the verified ID token.
	Nonce   string
	idToken *gooidc.IDToken
}

// Claims unmarshals the verified ID token claims into v.
// Returns an error if the TokenSet has no verified ID token.
func (t TokenSet) Claims(v any) error {
	if t.idToken == nil {
		return errors.New("auth/oidc: no verified id_token")
	}
	if err := t.idToken.Claims(v); err != nil {
		return fmt.Errorf("auth/oidc: decode claims: %w", err)
	}
	return nil
}

// UserInfo contains the claims from the OIDC UserInfo endpoint.
type UserInfo struct {
	Subject string
	Email   string
	Name    string
	Picture string
	// Extra holds all claims returned by the IdP.
	Extra map[string]any
}

// Provider wraps the go-oidc provider + oauth2 config.
type Provider struct {
	mu       sync.Mutex
	opts     Options
	provider *gooidc.Provider
	cfg      oauth2.Config
	verifier *gooidc.IDTokenVerifier
	logger   *slog.Logger
	client   *http.Client
}

func newProvider(opts Options, logger *slog.Logger) (*Provider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.withDefaults().DiscoveryTimeout)
	defer cancel()
	return NewProvider(ctx, opts, logger)
}

// NewProvider performs bounded OIDC discovery using opts.HTTPClient. The
// supplied context controls startup cancellation in addition to the configured
// discovery timeout. When opts.LazyDiscovery is true, discovery is deferred until
// the first call that requires it.
func NewProvider(ctx context.Context, opts Options, logger *slog.Logger) (*Provider, error) {
	opts = opts.withDefaults()
	if opts.IssuerURL == "" || opts.ClientID == "" || opts.RedirectURL == "" {
		return nil, errors.New("auth/oidc: issuer_url, client_id, and redirect_url are required")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	cfg := oauth2.Config{
		ClientID:     opts.ClientID,
		ClientSecret: opts.ClientSecret,
		RedirectURL:  opts.RedirectURL,
		Scopes:       opts.Scopes,
	}
	p := &Provider{
		opts:   opts,
		cfg:    cfg,
		logger: logger,
		client: opts.HTTPClient,
	}
	if opts.LazyDiscovery {
		return p, nil
	}
	if err := p.ensureDiscovery(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) ensureDiscovery(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.provider != nil {
		return nil
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, p.opts.DiscoveryTimeout)
	defer cancel()
	discoveryCtx = gooidc.ClientContext(discoveryCtx, p.client)

	prov, err := gooidc.NewProvider(discoveryCtx, p.opts.IssuerURL)
	if err != nil {
		return fmt.Errorf("auth/oidc: discover provider: %w", err)
	}
	p.provider = prov
	p.verifier = prov.Verifier(&gooidc.Config{ClientID: p.opts.ClientID})
	p.cfg.Endpoint = prov.Endpoint()
	p.logger.InfoContext(ctx, "auth/oidc: provider ready", slog.String("issuer", p.opts.IssuerURL))
	return nil
}

// AuthURL returns the IdP authorization URL and the PKCE verifier.
// Store verifier in the session before redirecting; pass it to
// [Exchange] on callback. Fails only when the OS entropy source does.
// On a lazy provider not yet discovered, AuthURL returns an error.
func (p *Provider) AuthURL(state string) (url, verifier string, err error) {
	p.mu.Lock()
	notDiscovered := p.opts.LazyDiscovery && p.provider == nil
	p.mu.Unlock()
	if notDiscovered {
		return "", "", errors.New("auth/oidc: lazy provider not discovered; use Begin")
	}
	return p.authCodeURL(state)
}

// authCodeURL builds the S256 PKCE authorization URL shared by AuthURL and Begin.
func (p *Provider) authCodeURL(state string, extra ...oauth2.AuthCodeOption) (url, verifier string, err error) {
	if state == "" {
		return "", "", errors.New("auth/oidc: state must not be empty")
	}
	verifier, err = pkceVerifier()
	if err != nil {
		return "", "", err
	}
	opts := append([]oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge(verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}, extra...)
	return p.cfg.AuthCodeURL(state, opts...), verifier, nil
}

// Begin initiates an OIDC authentication flow with PKCE and nonce.
// Store Verifier and Nonce from [AuthRequest] in the session, and pass them
// back to [Provider.Complete] upon callback.
func (p *Provider) Begin(ctx context.Context, state string) (AuthRequest, error) {
	if state == "" {
		return AuthRequest{}, errors.New("auth/oidc: state must not be empty")
	}
	if err := p.ensureDiscovery(ctx); err != nil {
		return AuthRequest{}, err
	}
	nonce, err := randomSecret("generate nonce")
	if err != nil {
		return AuthRequest{}, err
	}
	authURL, verifier, err := p.authCodeURL(state, gooidc.Nonce(nonce))
	if err != nil {
		return AuthRequest{}, err
	}
	return AuthRequest{URL: authURL, Verifier: verifier, Nonce: nonce}, nil
}

func (p *Provider) exchange(ctx context.Context, code, verifier string) (TokenSet, error) {
	ctx = gooidc.ClientContext(ctx, p.client)
	tok, err := p.cfg.Exchange(
		ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		return TokenSet{}, fmt.Errorf("auth/oidc: exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return TokenSet{}, errors.New("auth/oidc: no id_token in response")
	}
	idTok, verifyErr := p.verifier.Verify(ctx, rawID)
	if verifyErr != nil {
		return TokenSet{}, fmt.Errorf("%w: auth/oidc: verify id_token: %w", gerr.Unauthorized("invalid id_token"), verifyErr)
	}
	return TokenSet{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		IDToken:      rawID,
		Expiry:       tok.Expiry,
		Subject:      idTok.Subject,
		Nonce:        idTok.Nonce,
		idToken:      idTok,
	}, nil
}

// Exchange trades an authorization code for tokens. verifier must
// match the one returned by [AuthURL] for this session.
func (p *Provider) Exchange(ctx context.Context, code, verifier string) (TokenSet, error) {
	if err := p.ensureDiscovery(ctx); err != nil {
		return TokenSet{}, err
	}
	return p.exchange(ctx, code, verifier)
}

// Complete completes the authentication flow by exchanging code for tokens and
// validating the ID token nonce against req.Nonce.
func (p *Provider) Complete(ctx context.Context, code string, req AuthRequest) (TokenSet, error) {
	if err := p.ensureDiscovery(ctx); err != nil {
		return TokenSet{}, err
	}
	if req.Nonce == "" {
		return TokenSet{}, fmt.Errorf("%w: auth/oidc: nonce must not be empty", gerr.Unauthorized("empty nonce"))
	}
	toks, err := p.exchange(ctx, code, req.Verifier)
	if err != nil {
		return TokenSet{}, err
	}
	if toks.Nonce == "" || subtle.ConstantTimeCompare([]byte(req.Nonce), []byte(toks.Nonce)) != 1 {
		return TokenSet{}, fmt.Errorf("%w: auth/oidc: verify id_token: nonce mismatch", gerr.Unauthorized("nonce mismatch"))
	}
	return toks, nil
}

// UserInfo fetches claims from the IdP's UserInfo endpoint using the
// access token.
func (p *Provider) UserInfo(ctx context.Context, accessToken string) (UserInfo, error) {
	if err := p.ensureDiscovery(ctx); err != nil {
		return UserInfo{}, err
	}
	ctx = gooidc.ClientContext(ctx, p.client)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken})
	ui, err := p.provider.UserInfo(ctx, ts)
	if err != nil {
		return UserInfo{}, fmt.Errorf("auth/oidc: userinfo: %w", err)
	}
	var extra map[string]any
	if claimErr := ui.Claims(&extra); claimErr != nil {
		return UserInfo{}, fmt.Errorf("auth/oidc: decode claims: %w", claimErr)
	}
	info := UserInfo{
		Subject: ui.Subject,
		Email:   ui.Email,
		Extra:   extra,
	}
	if v, ok := extra["name"].(string); ok {
		info.Name = v
	}
	if v, ok := extra["picture"].(string); ok {
		info.Picture = v
	}
	return info, nil
}

// Module provides *oidc.Provider to the fx graph.
// Requires [Core] for config + log.
var Module = fx.Module(
	"golusoris.auth.oidc",
	fx.Provide(loadOptions),
	fx.Provide(newProvider),
)

// --- PKCE helpers ---

func randomSecret(op string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth/oidc: %s: %w", op, err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceVerifier() (string, error) {
	return randomSecret("pkce verifier")
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
