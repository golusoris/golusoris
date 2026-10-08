// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package credentials_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/container/registry/credentials"
	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
)

func loadConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: ".", Files: []string{path}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

func resource(t *testing.T, host string) authn.Resource {
	t.Helper()
	reg, err := name.NewRegistry(host)
	if err != nil {
		t.Fatalf("registry %q: %v", host, err)
	}
	return reg
}

func authOf(t *testing.T, kc authn.Keychain, host string) *authn.AuthConfig {
	t.Helper()
	a, err := authn.Resolve(t.Context(), kc, resource(t, host))
	if err != nil {
		t.Fatalf("resolve %s: %v", host, err)
	}
	cfg, err := authn.Authorization(t.Context(), a)
	if err != nil {
		t.Fatalf("authorization %s: %v", host, err)
	}
	return cfg
}

func TestKeychain(t *testing.T) {
	t.Parallel()
	boom := errors.New("token exchange failed")
	kc := credentials.Keychain(credentials.ProviderFunc(func(_ context.Context, host string) (credentials.Credential, error) {
		switch host {
		case "basic.example":
			return credentials.Credential{Username: "u", Password: "p"}, nil
		case "token.example":
			return credentials.Credential{RefreshToken: "rt", AccessToken: "at"}, nil
		case "broken.example":
			return credentials.Credential{}, boom
		default:
			return credentials.Credential{}, credentials.ErrNoCredential
		}
	}))
	if got := authOf(t, kc, "basic.example"); got.Username != "u" || got.Password != "p" {
		t.Fatalf("basic = %+v", got)
	}
	if got := authOf(t, kc, "token.example"); got.IdentityToken != "rt" || got.RegistryToken != "at" {
		t.Fatalf("token = %+v", got)
	}
	if a, err := kc.Resolve(resource(t, "other.example")); err != nil || a != authn.Anonymous {
		t.Fatalf("unknown host = %v, %v; want Anonymous", a, err)
	}
	if _, err := kc.Resolve(resource(t, "broken.example")); !errors.Is(err, boom) {
		t.Fatalf("provider error = %v, want surfaced", err)
	}
}

// TestModule_ChainOrder resolves static, cloud-group and docker-config
// hosts through the fx-built keychain.
func TestModule_ChainOrder(t *testing.T) {
	dir := t.TempDir()
	pass := secretFile(t, dir, "pass", "from-file")
	auth := base64.StdEncoding.EncodeToString([]byte("alice:from-docker"))
	secretFile(t, dir, "config.json", `{"auths":{"docker.example":{"auth":"`+auth+`"}}}`)
	t.Setenv("DOCKER_CONFIG", dir)
	cfg := loadConfig(t, `
container:
  registry:
    credentials:
      static:
        - host: static.example
          username: robot
          password_file: `+pass+`
`)
	var kc authn.Keychain
	app := fxtest.New(t,
		fx.Supply(cfg),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		credentials.Module,
		credentials.ProvideFn(func() credentials.Provider {
			return fixed("cloud.example", credentials.Credential{Password: "from-cloud"})
		}),
		fx.Populate(&kc),
	)
	app.RequireStart()
	defer app.RequireStop()
	if got := authOf(t, kc, "static.example"); got.Username != "robot" || got.Password != "from-file" {
		t.Errorf("static = %+v", got)
	}
	if got := authOf(t, kc, "cloud.example"); got.Password != "from-cloud" {
		t.Errorf("cloud = %+v", got)
	}
	if got := authOf(t, kc, "docker.example"); got.Username != "alice" || got.Password != "from-docker" {
		t.Errorf("docker = %+v", got)
	}
	if a, err := kc.Resolve(resource(t, "none.example")); err != nil || a != authn.Anonymous {
		t.Fatalf("none.example = %v, %v", a, err)
	}
}

func TestNewKeychain_DockerDisabledAndInvalidStatic(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	kc, err := credentials.NewKeychain(credentials.Options{DockerConfig: credentials.DockerConfigOptions{Disabled: true}}, nil, clk)
	if err != nil {
		t.Fatalf("NewKeychain: %v", err)
	}
	if a, rerr := kc.Resolve(resource(t, "index.docker.io")); rerr != nil || a != authn.Anonymous {
		t.Fatalf("docker disabled = %v, %v; want Anonymous", a, rerr)
	}
	_, err = credentials.NewKeychain(credentials.Options{Static: []credentials.StaticOptions{{Host: "h"}}}, nil, clk)
	if !errors.Is(err, credentials.ErrInvalidStatic) {
		t.Fatalf("err = %v, want ErrInvalidStatic", err)
	}
	if got := credentials.DefaultOptions().CacheSkew; got != credentials.DefaultCacheSkew {
		t.Fatalf("default skew = %v", got)
	}
}

// basicAuth demands robot:s3cret on every request.
func basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "robot" || p != "s3cret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TestModule_RegistryClient wires credentials.Module into registry.Module
// and pushes an artifact to a registry that requires the static secret.
func TestModule_RegistryClient(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(basicAuth(regsrv.New()))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	pass := secretFile(t, t.TempDir(), "pass", "s3cret\n")
	cfg := loadConfig(t, `
container:
  registry:
    credentials:
      docker_config:
        disabled: true
      static:
        - host: "`+u.Host+`"
          username: robot
          password_file: `+pass+`
`)
	var c *registry.Client
	app := fxtest.New(t,
		fx.Supply(cfg),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		credentials.Module,
		registry.Module,
		fx.Populate(&c),
	)
	app.RequireStart()
	defer app.RequireStop()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err = c.PushArtifact(ctx, u.Host+"/vmafx/scores:t", registry.Artifact{ArtifactType: "application/vnd.vmafx.score.v1+json"}); err != nil {
		t.Fatalf("PushArtifact through credentials chain: %v", err)
	}
}
