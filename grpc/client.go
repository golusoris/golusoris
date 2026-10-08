// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
)

const (
	defaultRetryInitialBackoff = 100 * time.Millisecond
	defaultRetryMaxBackoff     = time.Second
	defaultRetryMultiplier     = 2.0
)

var (
	errClientFilesWithoutTLS = errors.New("grpc: client cert_file/key_file/ca_file need grpc.client.tls")
	errNegativeClientConfig  = errors.New("grpc: client keepalive and retry values must not be negative")
)

// ClientConfig configures [ConnFactory] dials (grpc.client.*). The zero value
// dials plaintext without keepalive pings or retries, like [NewConnFactory].
type ClientConfig struct {
	// TLS turns on transport security; without CAFile the system roots
	// verify the server.
	TLS bool `koanf:"tls"`
	// CertFile is the client certificate presented for mTLS.
	CertFile string `koanf:"cert_file"`
	// KeyFile is the private key for CertFile.
	KeyFile string `koanf:"key_file"`
	// CAFile verifies the server. Files reload for each new connection.
	CAFile string `koanf:"ca_file"`
	// ServerName overrides the authority used for :authority and TLS
	// verification (default: the dial target's host).
	ServerName string `koanf:"server_name"`
	// Keepalive sends client pings on idle connections.
	Keepalive ClientKeepaliveConfig `koanf:"keepalive"`
	// Retry installs a default service config that retries UNAVAILABLE.
	Retry RetryConfig `koanf:"retry"`
}

// ClientKeepaliveConfig tunes client pings (grpc.client.keepalive.*). Keep
// Time at or above the server's keepalive.min_time (default 5m), or the
// server answers with GOAWAY "too_many_pings".
type ClientKeepaliveConfig struct {
	// Time pings the server after this idle period; zero disables pings.
	// grpc-go raises values below 10s to 10s.
	Time time.Duration `koanf:"time"`
	// Timeout closes the connection when a ping stays unacknowledged
	// (zero: grpc-go default 20s).
	Timeout time.Duration `koanf:"timeout"`
	// PermitWithoutStream pings even while no RPC is active.
	PermitWithoutStream bool `koanf:"permit_without_stream"`
}

// RetryConfig is the client retry policy (grpc.client.retry.*). It applies to
// every method and retries only UNAVAILABLE.
type RetryConfig struct {
	// MaxAttempts counts the first attempt; values up to 1 disable retries
	// and grpc-go caps larger values at 5.
	MaxAttempts int `koanf:"max_attempts"`
	// InitialBackoff is the first retry delay (default 100ms).
	InitialBackoff time.Duration `koanf:"initial_backoff"`
	// MaxBackoff caps the retry delay (default 1s).
	MaxBackoff time.Duration `koanf:"max_backoff"`
	// BackoffMultiplier grows the delay per attempt (default 2).
	BackoffMultiplier float64 `koanf:"backoff_multiplier"`
}

func (r RetryConfig) withDefaults() RetryConfig {
	r.InitialBackoff = durationOr(r.InitialBackoff, defaultRetryInitialBackoff)
	r.MaxBackoff = durationOr(r.MaxBackoff, defaultRetryMaxBackoff)
	if r.BackoffMultiplier == 0 {
		r.BackoffMultiplier = defaultRetryMultiplier
	}
	return r
}

func (c ClientConfig) validate() error {
	if !c.TLS && (c.CertFile != "" || c.KeyFile != "" || c.CAFile != "") {
		return errClientFilesWithoutTLS
	}
	k, r := c.Keepalive, c.Retry
	if k.Time < 0 || k.Timeout < 0 || r.InitialBackoff < 0 || r.MaxBackoff < 0 || r.BackoffMultiplier < 0 {
		return errNegativeClientConfig
	}
	return nil
}

// NewConnFactoryWithConfig returns a ConnFactory whose dials apply cfg: TLS
// from files reloaded per connection, client keepalive, and the retry
// policy. logger receives TLS reload results; nil uses slog.Default().
func NewConnFactoryWithConfig(cfg ClientConfig, logger *slog.Logger) (*ConnFactory, error) {
	return newClientFactory(cfg, logger, nil)
}

func newClientFactory(cfg ClientConfig, logger *slog.Logger, clk clock.Clock) (*ConnFactory, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	creds, err := clientCredentials(cfg, logger, clk)
	if err != nil {
		return nil, err
	}
	opts := []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithTransportCredentials(creds),
	}
	if cfg.ServerName != "" {
		opts = append(opts, grpc.WithAuthority(cfg.ServerName))
	}
	if k := cfg.Keepalive; k.Time > 0 {
		opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: k.Time, Timeout: k.Timeout, PermitWithoutStream: k.PermitWithoutStream,
		}))
	}
	if cfg.Retry.MaxAttempts > 1 {
		sc, err := retryServiceConfig(cfg.Retry.withDefaults())
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithDefaultServiceConfig(sc))
	}
	return &ConnFactory{dialOpts: opts}, nil
}

func clientCredentials(cfg ClientConfig, logger *slog.Logger, clk clock.Clock) (credentials.TransportCredentials, error) {
	switch {
	case !cfg.TLS:
		return insecure.NewCredentials(), nil
	case cfg.CertFile == "" && cfg.KeyFile == "" && cfg.CAFile == "":
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13}), nil
	}
	files := tlsx.Files{Cert: cfg.CertFile, Key: cfg.KeyFile, CA: cfg.CAFile}
	reloader, err := tlsx.NewReloader(files, tlsx.Options{Clock: clk, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("grpc: load client tls: %w", err)
	}
	return reloadingCreds{reloader: reloader}, nil
}

// retryServiceConfig renders the service-config JSON for a policy that
// covers every method ("name": [{}]).
func retryServiceConfig(r RetryConfig) (string, error) {
	policy := map[string]any{
		"maxAttempts":          r.MaxAttempts,
		"initialBackoff":       protoDuration(r.InitialBackoff),
		"maxBackoff":           protoDuration(r.MaxBackoff),
		"backoffMultiplier":    r.BackoffMultiplier,
		"retryableStatusCodes": []string{"UNAVAILABLE"},
	}
	sc := map[string]any{
		"methodConfig": []any{map[string]any{"name": []any{map[string]any{}}, "retryPolicy": policy}},
	}
	b, err := json.Marshal(sc)
	if err != nil {
		return "", fmt.Errorf("grpc: encode retry service config: %w", err)
	}
	return string(b), nil
}

// protoDuration formats d as a google.protobuf.Duration JSON string ("1.5s").
func protoDuration(d time.Duration) string {
	return fmt.Sprintf("%d.%09ds", d/time.Second, d%time.Second)
}
