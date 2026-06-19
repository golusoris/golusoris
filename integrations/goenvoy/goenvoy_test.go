package goenvoy_test

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/client"
	"github.com/golusoris/golusoris/integrations/goenvoy"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

const configYAML = `
integrations:
  goenvoy:
    services:
      sonarr:
        provider: sonarr
        base_url: http://sonarr:8989
        api_key: k3y
        timeout: 10s
        cache_ttl: 30s
        retry:
          max: 3
          wait: 100ms
        breaker:
          max: 5
      tmdb:
        provider: tmdb
        access_token: tok
        cache_ttl: 5m
`

// writeConfig writes a YAML config file and returns a *config.Config loaded
// from it. A file is used (not env) because the spec's leaf keys contain
// underscores (base_url, api_key, ...) which the env loader splits on '_'
// unless the app declares them as CompoundKeys.
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

func TestLoadOptions_fileRoundTrip(t *testing.T) {
	t.Parallel()
	cfg := writeConfig(t, configYAML)

	var opts goenvoy.Options
	if err := cfg.Unmarshal("integrations.goenvoy", &opts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	sonarr, ok := opts.Services["sonarr"]
	if !ok {
		t.Fatal("sonarr service missing")
	}
	if sonarr.Provider != "sonarr" || sonarr.BaseURL != "http://sonarr:8989" || sonarr.APIKey != "k3y" {
		t.Fatalf("sonarr basics: %+v", sonarr)
	}
	if sonarr.Timeout != 10*time.Second || sonarr.CacheTTL != 30*time.Second {
		t.Fatalf("durations: timeout=%v ttl=%v", sonarr.Timeout, sonarr.CacheTTL)
	}
	if sonarr.Retry.Max != 3 || sonarr.Retry.Wait != 100*time.Millisecond {
		t.Fatalf("retry: %+v", sonarr.Retry)
	}
	if sonarr.Breaker.Max != 5 {
		t.Fatalf("breaker: %+v", sonarr.Breaker)
	}
	if tmdb := opts.Services["tmdb"]; tmdb.AccessToken != "tok" || tmdb.CacheTTL != 5*time.Minute {
		t.Fatalf("tmdb: %+v", tmdb)
	}
}

// TestLoadOptions_envOverride proves env vars override file values when the app
// declares the underscore leaf keys as CompoundKeys (the realistic wiring for
// secret interpolation, e.g. APP_..._API_KEY).
func TestLoadOptions_envOverride(t *testing.T) {
	// t.Setenv forbids t.Parallel.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("APP_INTEGRATIONS_GOENVOY_SERVICES_SONARR_API_KEY", "env-key")
	cfg, err := config.New(config.Options{
		EnvPrefix: "APP_", Delimiter: ".", Watch: false, Files: []string{path},
		CompoundKeys: []string{"integrations.goenvoy.services.sonarr.api_key"},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var opts goenvoy.Options
	if err := cfg.Unmarshal("integrations.goenvoy", &opts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := opts.Services["sonarr"].APIKey; got != "env-key" {
		t.Fatalf("env override failed: api_key=%q", got)
	}
}

// TestDistinctHTTPClients guards the in-place-timeout-mutation hazard: every
// service must get its OWN *http.Client so goenvoy's WithTimeout can't corrupt
// another service's timeout.
func TestDistinctHTTPClients(t *testing.T) {
	t.Parallel()
	var built []*http.Client
	spy := func(o client.Options) *http.Client {
		hc := client.New(o)
		built = append(built, hc)
		return hc
	}
	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"a": {Provider: goenvoy.ProviderTMDb, AccessToken: "x", Timeout: 1 * time.Second},
		"b": {Provider: goenvoy.ProviderTMDb, AccessToken: "y", Timeout: 9 * time.Second},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, spy)

	if _, err := r.TMDb("a"); err != nil {
		t.Fatalf("TMDb a: %v", err)
	}
	if _, err := r.TMDb("b"); err != nil {
		t.Fatalf("TMDb b: %v", err)
	}
	if len(built) != 2 {
		t.Fatalf("expected 2 distinct http clients, got %d", len(built))
	}
	if built[0] == built[1] {
		t.Fatal("services share an *http.Client (timeout mutation hazard)")
	}
	if built[0].Timeout == built[1].Timeout {
		t.Fatalf("timeouts not independent: %v == %v", built[0].Timeout, built[1].Timeout)
	}
}

func TestRegistry_unknownService(t *testing.T) {
	t.Parallel()
	r := goenvoy.NewRegistryForTest(goenvoy.Options{}, discardLogger(), nil, nil, nil)
	if _, err := r.Sonarr("nope"); err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func TestRegistry_caches(t *testing.T) {
	t.Parallel()
	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"tmdb": {Provider: goenvoy.ProviderTMDb, AccessToken: "x"},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	c1, err := r.TMDb("tmdb")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	c2, err := r.TMDb("tmdb")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if c1 != c2 {
		t.Fatal("expected repeated lookup to return the same client")
	}
	if names := r.Names(); len(names) != 1 || names[0] != "tmdb" {
		t.Fatalf("Names: %v", names)
	}
}
