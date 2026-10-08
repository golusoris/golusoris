// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package credentials

import (
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
)

// Group is the fx value group cloud providers join; [Module] places its
// members between the static entries and the docker config keychain.
const Group = `group:"container.registry.credentials"`

// Options is the koanf config under "container.registry.credentials".
type Options struct {
	// Static binds hosts to mounted secret files; checked first.
	Static []StaticOptions `koanf:"static"`
	// DockerConfig controls the docker config.json fallback.
	DockerConfig DockerConfigOptions `koanf:"docker_config"`
	// CacheSkew refreshes expiring cloud tokens this long before expiry.
	CacheSkew time.Duration `koanf:"cache_skew"`
}

// DockerConfigOptions controls the [authn.DefaultKeychain] fallback, which
// reads $DOCKER_CONFIG/config.json or ~/.docker/config.json and runs
// configured credential helpers.
type DockerConfigOptions struct {
	// Disabled drops the fallback (workload-identity-only deployments).
	Disabled bool `koanf:"disabled"`
}

// DefaultOptions returns the defaults [Module] starts from.
func DefaultOptions() Options {
	return Options{CacheSkew: DefaultCacheSkew}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("container.registry.credentials", &opts); err != nil {
		return Options{}, fmt.Errorf("credentials: load options: %w", err)
	}
	return opts, nil
}

// NewKeychain builds static -> cloud providers (cached) -> docker config.
func NewKeychain(opts Options, cloud []Provider, clk clock.Clock) (authn.Keychain, error) {
	static, err := NewStaticFiles(opts.Static)
	if err != nil {
		return nil, err
	}
	chain := make(Chain, 0, len(cloud)+1)
	chain = append(chain, static)
	chain = append(chain, cloud...)
	kc := Keychain(NewCache(chain, clk, opts.CacheSkew))
	if opts.DockerConfig.Disabled {
		return authn.NewMultiKeychain(kc), nil
	}
	return authn.NewMultiKeychain(kc, authn.DefaultKeychain), nil
}

type keychainParams struct {
	fx.In

	Opts  Options
	Clock clock.Clock
	Cloud []Provider `group:"container.registry.credentials"`
}

func newKeychain(p keychainParams) (authn.Keychain, error) {
	return NewKeychain(p.Opts, p.Cloud, p.Clock)
}

// ProvideFn adds a [Provider] constructor to [Group]. Cloud sub-packages use
// it; applications may add their own providers the same way.
func ProvideFn(constructor any) fx.Option {
	return fx.Provide(fx.Annotate(constructor, fx.As(new(Provider)), fx.ResultTags(Group)))
}

// Module provides the [authn.Keychain] that registry.Module injects into
// its client.
//
//	fx.New(
//	    config.Module, clock.Module,
//	    credentials.Module, ecr.Module, // ECR via IRSA / Pod Identity
//	    registry.Module,
//	)
//
// Config (prefix "container.registry.credentials"):
//
//	static[0].host          = "harbor.example.com"
//	static[0].username      = "robot$vmafx"
//	static[0].password_file = "/var/run/secrets/harbor/token"
//	docker_config.disabled  = false
//	cache_skew              = "5m"
var Module = fx.Module(
	"golusoris.container.registry.credentials",
	fx.Provide(loadOptions),
	fx.Provide(newKeychain),
)
