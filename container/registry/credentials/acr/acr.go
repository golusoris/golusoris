// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package acr resolves Azure Container Registry credentials from a Microsoft
// Entra token (AKS workload identity, managed identity, environment, Azure
// CLI via azidentity's default chain). The Entra access token is exchanged
// at https://<registry>/oauth2/exchange for an ACR refresh token, which the
// registry accepts as the password of the null-GUID user.
package acr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/container/registry/credentials"
	"github.com/golusoris/golusoris/core/config"
)

// Scope is the Entra scope for registry data-plane tokens.
const Scope = "https://containerregistry.azure.net/.default"

// Username is the null GUID that marks an ACR refresh token login.
const Username = "00000000-0000-0000-0000-000000000000"

// DefaultTimeout bounds one token exchange when Options.Timeout is zero.
const DefaultTimeout = 30 * time.Second

// maxExchangeBytes caps the exchange response body.
const maxExchangeBytes = 1 << 20

// ErrExchange reports a failed refresh-token exchange.
var ErrExchange = errors.New("acr: token exchange failed")

// hostSuffixes are the ACR login-server domains of the public and
// sovereign clouds.
var hostSuffixes = []string{".azurecr.io", ".azurecr.cn", ".azurecr.us"}

// Options is the koanf config under "container.registry.credentials.acr".
type Options struct {
	// TenantID is sent with the exchange; empty lets ACR use the token's
	// tenant.
	TenantID string `koanf:"tenant_id"`
	// Timeout bounds one exchange round trip.
	Timeout time.Duration `koanf:"timeout"`
}

// Provider implements [credentials.Provider] for ACR hosts.
type Provider struct {
	cred     azcore.TokenCredential
	opts     Options
	client   *http.Client
	endpoint func(host string) string
}

// New builds a provider. client nil uses an [http.Client] bounded by
// Options.Timeout.
func New(cred azcore.TokenCredential, opts Options, client *http.Client) *Provider {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}
	return &Provider{
		cred:     cred,
		opts:     opts,
		client:   client,
		endpoint: func(host string) string { return "https://" + host + "/oauth2/exchange" },
	}
}

// IsACRHost reports whether host is an ACR login server.
func IsACRHost(host string) bool {
	for _, s := range hostSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

// Credential implements [credentials.Provider].
func (p *Provider) Credential(ctx context.Context, host string) (credentials.Credential, error) {
	if !IsACRHost(host) {
		return credentials.Credential{}, credentials.ErrNoCredential
	}
	ctx, cancel := context.WithTimeout(ctx, p.opts.Timeout)
	defer cancel()
	aad, err := p.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{Scope}})
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("acr: entra token for %s: %w", host, err)
	}
	refresh, err := p.exchange(ctx, host, aad.Token)
	if err != nil {
		return credentials.Credential{}, err
	}
	return credentials.Credential{Username: Username, Password: refresh, ExpiresAt: aad.ExpiresOn}, nil
}

// exchange trades an Entra access token for an ACR refresh token.
func (p *Provider) exchange(ctx context.Context, host, accessToken string) (_ string, err error) {
	form := url.Values{"grant_type": {"access_token"}, "service": {host}, "access_token": {accessToken}}
	if p.opts.TenantID != "" {
		form.Set("tenant", p.opts.TenantID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(host), strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("acr: build exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("acr: exchange with %s: %w", host, err)
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxExchangeBytes))
	if err != nil {
		return "", fmt.Errorf("acr: read exchange response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: %s returned %d", ErrExchange, host, resp.StatusCode)
	}
	var out struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err = json.Unmarshal(body, &out); err != nil || out.RefreshToken == "" {
		return "", fmt.Errorf("%w: %s: no refresh_token in response", ErrExchange, host)
	}
	return out.RefreshToken, nil
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := Options{Timeout: DefaultTimeout}
	if err := cfg.Unmarshal("container.registry.credentials.acr", &opts); err != nil {
		return Options{}, fmt.Errorf("acr: load options: %w", err)
	}
	return opts, nil
}

func newProvider(opts Options) (*Provider, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("acr: default azure credential: %w", err)
	}
	return New(cred, opts, nil), nil
}

// Module adds the ACR provider to the container/registry/credentials chain. On AKS with
// workload identity, azidentity reads AZURE_CLIENT_ID, AZURE_TENANT_ID and
// AZURE_FEDERATED_TOKEN_FILE injected by the webhook.
//
// Config (prefix "container.registry.credentials.acr"): tenant_id, timeout.
var Module = fx.Module(
	"golusoris.container.registry.credentials.acr",
	fx.Provide(loadOptions),
	credentials.ProvideFn(newProvider),
)
