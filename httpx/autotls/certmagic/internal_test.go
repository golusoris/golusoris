// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package certmagic

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cm "github.com/caddyserver/certmagic"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
)

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Domains) != 0 {
		t.Errorf("Domains = %v, want empty", opts.Domains)
	}
	if opts.Staging {
		t.Error("Staging should be false by default")
	}
	if opts.Email != "" {
		t.Errorf("Email = %q, want empty", opts.Email)
	}
	if opts.Timeout != DefaultStartTimeout {
		t.Errorf("Timeout = %s, want %s", opts.Timeout, DefaultStartTimeout)
	}
}

func TestOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := Options{
		Domains: []string{"example.com"},
		Email:   "admin@example.com",
		Staging: true,
		Timeout: time.Minute,
	}
	if opts.Domains[0] != "example.com" {
		t.Error("Domains not preserved")
	}
	if opts.Email != "admin@example.com" {
		t.Error("Email not preserved")
	}
	if !opts.Staging {
		t.Error("Staging not preserved")
	}
	if opts.Timeout != time.Minute {
		t.Errorf("Timeout = %s, want 1m", opts.Timeout)
	}
}

func TestNormalizeOptionsClonesDomains(t *testing.T) {
	t.Parallel()

	domains := []string{" example.test "}
	opts, err := normalizeOptions(Options{Domains: domains})
	if err != nil {
		t.Fatalf("normalizeOptions: %v", err)
	}
	domains[0] = "mutated.example.test"
	if got := opts.Domains[0]; got != "example.test" {
		t.Fatalf("Domains[0] = %q", got)
	}
}

func TestManagerStartUsesOwnedDomains(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{Domains: []string{"example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 1)
	manager := newManager(opts, managerRuntime{
		tlsConfig: new(tls.Config),
		manageSync: func(_ context.Context, domains []string) error {
			seen <- domains[0]
			return nil
		},
		stopCache: func() {},
	})
	t.Cleanup(func() { _ = manager.Close() })
	opts.Domains[0] = "mutated.example.test"

	if err = manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := <-seen; got != "example.test" {
		t.Fatalf("managed domain = %q", got)
	}
}

func TestManagerStartUsesFiniteTimeout(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{
		Domains: []string{"example.test"},
		Timeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newManager(opts, managerRuntime{
		tlsConfig: new(tls.Config),
		manageSync: func(ctx context.Context, _ []string) error {
			<-ctx.Done()
			return ctx.Err()
		},
		stopCache: func() {},
	})
	t.Cleanup(func() { _ = manager.Close() })

	err = manager.Start(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start: got %v", err)
	}
}

func TestManagerStartHonorsCancellation(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{Domains: []string{"example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	manager := newManager(opts, managerRuntime{
		tlsConfig: new(tls.Config),
		manageSync: func(ctx context.Context, _ []string) error {
			<-ctx.Done()
			return ctx.Err()
		},
		stopCache: func() {},
	})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = manager.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start: got %v", err)
	}
}

func TestManagerCloseCancelsStartAndStopsCacheOnce(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{Domains: []string{"example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	var stops atomic.Int32
	manager := newManager(opts, managerRuntime{
		tlsConfig: new(tls.Config),
		manageSync: func(ctx context.Context, _ []string) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		},
		stopCache: func() { stops.Add(1) },
	})
	startErr := make(chan error, 1)
	go func() { startErr <- manager.Start(context.Background()) }()
	<-entered

	if err = manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !errors.Is(<-startErr, context.Canceled) {
		t.Fatal("Start did not stop with context cancellation")
	}
	if err = manager.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("cache stops = %d, want 1", got)
	}
}

func TestLifecycleStartsAndClosesManager(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{Domains: []string{"example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	var starts atomic.Int32
	var stops atomic.Int32
	manager := newManager(opts, managerRuntime{
		tlsConfig: new(tls.Config),
		manageSync: func(context.Context, []string) error {
			starts.Add(1)
			return nil
		},
		stopCache: func() { stops.Add(1) },
	})
	lifecycle := fxtest.NewLifecycle(t)
	registerLifecycle(lifecycle, manager)
	lifecycle.RequireStart().RequireStop()
	if starts.Load() != 1 || stops.Load() != 1 {
		t.Fatalf("starts=%d stops=%d", starts.Load(), stops.Load())
	}
}

func TestLifecycleClosesManagerAfterStartFailure(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{Domains: []string{"example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	var stops atomic.Int32
	manager := newManager(opts, managerRuntime{
		tlsConfig:  new(tls.Config),
		manageSync: func(context.Context, []string) error { return errors.New("boom") },
		stopCache:  func() { stops.Add(1) },
	})
	lifecycle := fxtest.NewLifecycle(t)
	registerLifecycle(lifecycle, manager)
	if err = lifecycle.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Start: got %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("cache stops = %d, want 1", got)
	}
}

func TestModuleGraph(t *testing.T) {
	t.Parallel()

	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	if err = fx.ValidateApp(fx.Supply(cfg), Module); err != nil {
		t.Fatalf("ValidateApp: %v", err)
	}
}

func TestRuntimeRetainsLocalConfigForRenewal(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{
		Domains: []string{"example.test"},
		Email:   "ops@example.test",
		Staging: true,
		Storage: &cm.FileStorage{Path: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := buildRuntime(opts)
	t.Cleanup(runtime.stopCache)

	got, err := runtime.configForCert(cm.Certificate{})
	if err != nil {
		t.Fatalf("configForCert: %v", err)
	}
	if got != runtime.config {
		t.Fatal("renewal callback did not return manager-local config")
	}
	issuer, ok := got.Issuers[0].(*cm.ACMEIssuer)
	if !ok {
		t.Fatalf("issuer type = %T", got.Issuers[0])
	}
	if issuer.CA != cm.LetsEncryptStagingCA || issuer.Email != opts.Email || !issuer.Agreed {
		t.Fatalf("issuer = %+v", issuer)
	}
	if !issuer.DisableHTTPChallenge {
		t.Fatal("HTTP challenge should be disabled")
	}
}

func TestIssuerDoesNotInheritMutableACMEDefaults(t *testing.T) {
	t.Parallel()
	opts, err := normalizeOptions(Options{
		Domains: []string{"example.test"},
		Storage: &cm.FileStorage{Path: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := buildRuntime(opts)
	t.Cleanup(runtime.stopCache)
	issuer := runtime.config.Issuers[0].(*cm.ACMEIssuer)
	if issuer.Email != " " || issuer.DisableTLSALPNChallenge || issuer.HTTPProxy == nil {
		t.Fatalf("issuer retained process defaults: %+v", issuer)
	}
}
