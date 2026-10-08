// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package credentials

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
)

// ResolveTimeout bounds a context-less [authn.Keychain.Resolve] lookup.
const ResolveTimeout = 30 * time.Second

// providerKeychain adapts a [Provider] to [authn.Keychain] and
// [authn.ContextKeychain].
type providerKeychain struct {
	p Provider
}

// Keychain adapts p for go-containerregistry: a credential becomes an
// [authn.AuthConfig], [ErrNoCredential] becomes [authn.Anonymous] so an
// [authn.NewMultiKeychain] moves on, and any other error is returned.
// Hosts are [authn.Resource.RegistryStr] values ("ghcr.io",
// "harbor.example.com:8443", "index.docker.io" for Docker Hub).
func Keychain(p Provider) authn.Keychain {
	return providerKeychain{p: p}
}

// Resolve implements [authn.Keychain] for callers without a context; the
// lookup is bounded by [ResolveTimeout].
func (k providerKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ResolveTimeout)
	defer cancel()
	return k.ResolveContext(ctx, target)
}

// ResolveContext implements [authn.ContextKeychain].
func (k providerKeychain) ResolveContext(ctx context.Context, target authn.Resource) (authn.Authenticator, error) {
	cred, err := k.p.Credential(ctx, target.RegistryStr())
	if errors.Is(err, ErrNoCredential) {
		return authn.Anonymous, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credentials: %s: %w", target.RegistryStr(), err)
	}
	return authn.FromConfig(authn.AuthConfig{
		Username:      cred.Username,
		Password:      cred.Password,
		IdentityToken: cred.RefreshToken,
		RegistryToken: cred.AccessToken,
	}), nil
}
