// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package redis

import (
	"crypto/tls"
	"log/slog"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/config"
)

func TestNewClient_RejectsNilLoggerBeforeDial(t *testing.T) {
	t.Parallel()
	c, err := newClient(Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "logger") {
		t.Fatalf("newClient error = %v; want logger dependency error", err)
	}
	if c != nil {
		t.Fatal("newClient returned a client with nil logger")
	}
}

func TestNewClientRejectsInvalidConfigBeforeDial(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	for name, opts := range map[string]Options{
		"blank address":   {Addr: "   "},
		"empty member":    {Addr: "localhost:6379, ,localhost:6380"},
		"missing port":    {Addr: "localhost"},
		"negative db":     {Addr: "localhost:6379", DB: -1},
		"redis URL input": {Addr: "redis://localhost:6379"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := newClient(opts, logger)
			if err == nil || client != nil {
				t.Fatalf("newClient() = (%v, %v), want validation error", client, err)
			}
		})
	}
}

func TestClientOptionEnablesVerifiedTLS(t *testing.T) {
	t.Parallel()

	got := clientOption(Options{Addr: " cache.example.invalid:6379 ", TLS: true})
	if got.TLSConfig == nil {
		t.Fatal("TLSConfig = nil, want verified TLS")
	}
	if got.TLSConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify = true, want certificate verification")
	}
	if got.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2", got.TLSConfig.MinVersion)
	}
	if len(got.InitAddress) != 1 || got.InitAddress[0] != "cache.example.invalid:6379" {
		t.Errorf("InitAddress = %q, want trimmed address", got.InitAddress)
	}
}

func TestClientOptionLeavesTLSDisabledByDefault(t *testing.T) {
	t.Parallel()

	got := clientOption(Options{Addr: "localhost:6379"})
	if got.TLSConfig != nil {
		t.Errorf("TLSConfig = %#v, want nil", got.TLSConfig)
	}
}

func TestDefaultOptions_addr(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.Addr != "localhost:6379" {
		t.Errorf("Addr = %q, want \"localhost:6379\"", opts.Addr)
	}
}

func TestDefaultOptions_zeroValues(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.Username != "" {
		t.Errorf("Username = %q, want \"\"", opts.Username)
	}
	if opts.Password != "" {
		t.Errorf("Password = %q, want \"\"", opts.Password)
	}
	if opts.DB != 0 {
		t.Errorf("DB = %d, want 0", opts.DB)
	}
	if opts.TLS {
		t.Error("TLS = true, want false")
	}
}

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_REDIS_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr != "localhost:6379" {
		t.Errorf("Addr = %q, want \"localhost:6379\"", opts.Addr)
	}
}
