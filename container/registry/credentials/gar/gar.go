// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package gar resolves Google Artifact Registry (and legacy Container
// Registry) credentials from Application Default Credentials: GKE Workload
// Identity via the metadata server, workload identity federation config
// files, or gcloud user credentials. The OAuth2 access token is sent as the
// password of the fixed user "oauth2accesstoken".
package gar

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/fx"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/golusoris/golusoris/container/registry/credentials"
)

// Scope is the OAuth2 scope Artifact Registry accepts.
const Scope = "https://www.googleapis.com/auth/cloud-platform"

// Username is the fixed registry user paired with an access token.
const Username = "oauth2accesstoken"

// tokenTimeout bounds one credential lookup, ADC discovery included (HISS-02).
const tokenTimeout = 30 * time.Second

// Provider implements [credentials.Provider] for Google registry hosts.
type Provider struct {
	source func(ctx context.Context) (oauth2.TokenSource, error)
}

// New wraps a fixed OAuth2 token source; it should be reusing (caching).
func New(tokens oauth2.TokenSource) *Provider {
	return &Provider{source: func(context.Context) (oauth2.TokenSource, error) { return tokens, nil }}
}

// NewDefault builds a provider that discovers Application Default
// Credentials on each lookup under the lookup's deadline, because
// [oauth2.TokenSource.Token] takes no context. Wrap it in
// [credentials.Cache] (as [credentials.Module] does) so discovery runs once
// per token lifetime.
func NewDefault() *Provider {
	client := &http.Client{Timeout: tokenTimeout}
	return &Provider{source: func(ctx context.Context) (oauth2.TokenSource, error) {
		ts, err := google.DefaultTokenSource(context.WithValue(ctx, oauth2.HTTPClient, client), Scope)
		if err != nil {
			return nil, fmt.Errorf("gar: application default credentials: %w", err)
		}
		return ts, nil
	}}
}

// IsGoogleHost reports whether host is an Artifact Registry
// ("<location>-docker.pkg.dev") or Container Registry ("gcr.io",
// "<region>.gcr.io") endpoint.
func IsGoogleHost(host string) bool {
	return strings.HasSuffix(host, "-docker.pkg.dev") || host == "gcr.io" || strings.HasSuffix(host, ".gcr.io")
}

// Credential implements [credentials.Provider].
func (p *Provider) Credential(ctx context.Context, host string) (credentials.Credential, error) {
	if !IsGoogleHost(host) {
		return credentials.Credential{}, credentials.ErrNoCredential
	}
	ctx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	ts, err := p.source(ctx)
	if err != nil {
		return credentials.Credential{}, err
	}
	tok, err := ts.Token()
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("gar: access token for %s: %w", host, err)
	}
	if tok.AccessToken == "" {
		return credentials.Credential{}, fmt.Errorf("gar: access token for %s: empty token", host)
	}
	return credentials.Credential{Username: Username, Password: tok.AccessToken, ExpiresAt: tok.Expiry}, nil
}

// Module adds the Artifact Registry provider to the container/registry/credentials chain.
// Missing ADC surfaces as an error on the first lookup of a Google host.
//
//	fx.New(config.Module, clock.Module, credentials.Module, gar.Module, artifact.Module)
var Module = fx.Module(
	"golusoris.container.registry.credentials.gar",
	credentials.ProvideFn(NewDefault),
)
