// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/core/config"
)

const configYAML = `
container:
  registry:
    user_agent: test-agent/1.0
    timeout: 5s
`

// writeConfig writes body to a temp file and loads it as a [*config.Config],
// mirroring the convention used across the framework's other fx-wired
// packages (see integrations/goenvoy).
func writeConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: ".", Watch: false, Files: []string{path}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

// TestModule_StartsAndProvidesClient boots Module via fxtest, covering
// loadOptions and newClient, and exercises the provided *Client against a
// real in-process registry.
func TestModule_StartsAndProvidesClient(t *testing.T) {
	t.Parallel()
	cfg := writeConfig(t, configYAML)

	var c *registry.Client
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() authn.Keychain { return authn.NewMultiKeychain() }),
		registry.Module,
		fx.Populate(&c),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}()
	if c == nil {
		t.Fatal("expected *registry.Client to be populated")
	}

	host := newTestRegistry(t)
	img := mustRandomImage(t)
	ref := host + "/test/wired:v1"
	pushImage(t, ref, img)
	if _, err := c.Resolve(t.Context(), ref); err != nil {
		t.Fatalf("Resolve via wired client: %v", err)
	}
}

// staticKeychain always resolves to the same [authn.Authenticator],
// regardless of target — enough to prove an app-provided fx.Keychain reaches
// the wired [*registry.Client].
type staticKeychain struct{ auth authn.Authenticator }

func (k staticKeychain) Resolve(authn.Resource) (authn.Authenticator, error) { return k.auth, nil }

// TestModule_KeychainOverride asserts that an app-provided authn.Keychain —
// wired as an optional fx dependency of Module — is the one the client
// actually authenticates with, by inspecting the Authorization header a raw
// HTTP stub receives.
func TestModule_KeychainOverride(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", string(types.OCIManifestSchema1))
		w.Header().Set("Docker-Content-Digest", "sha256:"+fixedDigestHex)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse stub URL: %v", err)
	}

	cfg := writeConfig(t, configYAML)
	kc := staticKeychain{auth: &authn.Basic{Username: "user", Password: "pass"}}

	var c *registry.Client
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() authn.Keychain { return kc }),
		registry.Module,
		fx.Populate(&c),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}()

	ref := u.Host + "/test/basic:v1"
	if _, err := c.Resolve(t.Context(), ref); err != nil {
		t.Fatalf("Resolve via wired client: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if gotAuth != want {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, want)
	}
}

// fixedDigestHex is a syntactically valid sha256 hex used only to satisfy
// the HEAD response's required Docker-Content-Digest header; TestModule_
// KeychainOverride never validates it against real content.
var fixedDigestHex = strings.Repeat("0", 64)
