// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package credentials resolves registry credentials for
// [github.com/golusoris/golusoris/container/registry] behind its injectable
// [authn.Keychain] seam: an ordered provider chain of secret files, then
// cloud workload-identity providers (sub-packages ecr, gar, acr), then the
// docker config.json keychain ([authn.DefaultKeychain]).
//
// A [Provider] answers for one registry host. It returns [ErrNoCredential]
// when it has nothing for that host, so a [Chain] moves on to the next
// provider; any other error stops the chain and surfaces to the caller
// instead of silently degrading to anonymous access. [Keychain] adapts a
// Provider to go-containerregistry.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golusoris/golusoris/core/validate"
)

// ErrNoCredential reports that a provider holds no credential for a host.
var ErrNoCredential = errors.New("credentials: no credential for host")

// Credential authenticates against one registry. Exactly one of the
// username/password pair, RefreshToken, or AccessToken is normally set.
type Credential struct {
	// Username for basic or token-service authentication.
	Username string
	// Password is the secret paired with Username.
	Password string
	// RefreshToken is an identity token exchanged at the registry's token
	// service for short-lived access tokens.
	RefreshToken string
	// AccessToken is a registry bearer token sent as-is.
	AccessToken string
	// ExpiresAt is when the credential stops working. Zero means unknown;
	// [Cache] never caches such credentials.
	ExpiresAt time.Time
}

// IsZero reports whether c carries no secret material.
func (c Credential) IsZero() bool {
	return c.Username == "" && c.Password == "" && c.RefreshToken == "" && c.AccessToken == ""
}

// Provider resolves the credential for a registry host ("host" or
// "host:port"). It returns [ErrNoCredential] when it does not serve host.
type Provider interface {
	Credential(ctx context.Context, host string) (Credential, error)
}

// ProviderFunc adapts a function to [Provider].
type ProviderFunc func(ctx context.Context, host string) (Credential, error)

// Credential implements [Provider].
func (f ProviderFunc) Credential(ctx context.Context, host string) (Credential, error) {
	return f(ctx, host)
}

// Chain asks each provider in order and returns the first credential found.
// Nil entries are skipped. When every provider returns [ErrNoCredential], so
// does the chain.
type Chain []Provider

// Credential implements [Provider].
func (c Chain) Credential(ctx context.Context, host string) (Credential, error) {
	for _, p := range c {
		if validate.IsNil(p) {
			continue
		}
		cred, err := p.Credential(ctx, host)
		if errors.Is(err, ErrNoCredential) {
			continue
		}
		if err != nil {
			return Credential{}, fmt.Errorf("credentials: resolve %q: %w", host, err)
		}
		return cred, nil
	}
	return Credential{}, ErrNoCredential
}
