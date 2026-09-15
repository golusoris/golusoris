// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

// loadOptions unmarshals the "container.registry" config prefix into Options.
func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("container.registry", &opts); err != nil {
		return Options{}, fmt.Errorf("registry: load options: %w", err)
	}
	return opts, nil
}

// clientParams are the fx inputs for [newClient]. Keychain and Transport are
// optional so the module works without an app-provided override — [New]
// supplies the same defaults ([authn.DefaultKeychain], [http.DefaultTransport])
// either way.
type clientParams struct {
	fx.In

	Opts      Options
	Keychain  authn.Keychain    `optional:"true"`
	Transport http.RoundTripper `optional:"true"`
}

func newClient(p clientParams) *Client {
	return New(p.Opts, p.Keychain, p.Transport)
}

// Module provides a *[Client] to the fx graph. No init() side effects — all
// construction happens in fx constructors.
//
// Usage:
//
//	fx.New(
//	    config.Module,
//	    registry.Module,   // provides *registry.Client
//	    fx.Invoke(func(c *registry.Client) error {
//	        tags, err := c.ListTags(ctx, "gcr.io/my-proj/my-image")
//	        ...
//	    }),
//	)
//
// Config keys live under the "container.registry" prefix:
//
//	container.registry.user_agent = "my-app/1.0"
//	container.registry.timeout    = "15s"
//
// An app that needs a non-default [authn.Keychain] or [http.RoundTripper]
// (a cloud credential helper, mTLS, a proxy) provides one itself — fx wires
// it in automatically since both are optional dependencies of [newClient]:
//
//	fx.Provide(func() authn.Keychain { return authn.NewMultiKeychain(...) }),
var Module = fx.Module(
	"golusoris.container.registry",
	fx.Provide(loadOptions),
	fx.Provide(newClient),
)
