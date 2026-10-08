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
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
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

// TokenSet holds the tokens returned after a successful code exchange.
type TokenSet struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	Expiry       time.Time
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
	provider *gooidc.Provider
	cfg      oauth2.Config
	verifier *gooidc.IDTokenVerifier
	logger   *slog.Logger
	client   *http.Client
}

func newProvider(opts Options, logger *slog.Logger) (*Provider, error) {
	return NewProvider(context.Background(), opts, logger)
}

// NewProvider performs bounded OIDC discovery using opts.HTTPClient. The
// supplied context controls startup cancellation in addition to the configured
// discovery timeout.
func NewProvider(ctx context.Context, opts Options, logger *slog.Logger) (*Provider, error) {
	opts = opts.withDefaults()
	if opts.IssuerURL == "" || opts.ClientID == "" || opts.RedirectURL == "" {
		return nil, errors.New("auth/oidc: issuer_url, client_id, and redirect_url are required")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, opts.DiscoveryTimeout)
	defer cancel()
	discoveryCtx = gooidc.ClientContext(discoveryCtx, opts.HTTPClient)
	prov, err := gooidc.NewProvider(discoveryCtx, opts.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("auth/oidc: discover provider: %w", err)
	}
	verifier := prov.Verifier(&gooidc.Config{ClientID: opts.ClientID})
	cfg := oauth2.Config{
		ClientID:     opts.ClientID,
		ClientSecret: opts.ClientSecret,
		RedirectURL:  opts.RedirectURL,
		Endpoint:     prov.Endpoint(),
		Scopes:       opts.Scopes,
	}
	logger.InfoContext(ctx, "auth/oidc: provider ready", slog.String("issuer", opts.IssuerURL))
	return &Provider{provider: prov, cfg: cfg, verifier: verifier, logger: logger, client: opts.HTTPClient}, nil
}

// AuthURL returns the IdP authorization URL and the PKCE verifier.
// Store verifier in the session before redirecting; pass it to
// [Exchange] on callback. Fails only when the OS entropy source does.
func (p *Provider) AuthURL(state string) (url, verifier string, err error) {
	if state == "" {
		return "", "", errors.New("auth/oidc: state must not be empty")
	}
	verifier, err = pkceVerifier()
	if err != nil {
		return "", "", err
	}
	challenge := pkceChallenge(verifier)
	url = p.cfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return url, verifier, nil
}

// Exchange trades an authorization code for tokens. verifier must
// match the one returned by [AuthURL] for this session.
func (p *Provider) Exchange(ctx context.Context, code, verifier string) (TokenSet, error) {
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
	if _, verifyErr := p.verifier.Verify(ctx, rawID); verifyErr != nil {
		return TokenSet{}, fmt.Errorf("%w: auth/oidc: verify id_token: %w", gerr.Unauthorized("invalid id_token"), verifyErr)
	}
	return TokenSet{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		IDToken:      rawID,
		Expiry:       tok.Expiry,
	}, nil
}

// UserInfo fetches claims from the IdP's UserInfo endpoint using the
// access token.
func (p *Provider) UserInfo(ctx context.Context, accessToken string) (UserInfo, error) {
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

func pkceVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth/oidc: pkce verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
