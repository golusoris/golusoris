// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package nats provides an fx-wired NATS JetStream client via nats-io/nats.go.
//
// Usage:
//
//	fx.New(nats.Module) // requires "nats.*" koanf config
//
// Config keys (koanf prefix "nats"):
//
//	url:        "nats://localhost:4222"
//	name:       "my-service"
//	creds:      "/var/run/secrets/nats/user.creds" # JWT + NKey seed
//	nkey:       "/var/run/secrets/nats/user.nk"    # NKey seed; excludes creds
//	tls:        {ca: ca.crt, cert: tls.crt, key: tls.key}
//	acktimeout: 5s # JetStream PubAck wait for PublishCloudEvent
//
// Leaf keys are single words because the env mapping splits on every
// underscore (APP_NATS_TLS_CA -> nats.tls.ca).
package nats

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/tlsx"
)

const (
	defaultPublishFlushTimeout = 5 * time.Second
	// DefaultAckTimeout bounds one JetStream publish, PubAck included, when
	// Config.AckTimeout is zero.
	DefaultAckTimeout = 5 * time.Second
	// TLSConfigName is the fx name of the optional *tls.Config added by
	// [ProvideTLSConfig].
	TLSConfigName = "golusoris.nats.tls"
)

var (
	// ErrConflictingAuth reports both a creds file and an NKey seed file.
	ErrConflictingAuth = errors.New("nats: creds and nkey are mutually exclusive")
	// ErrConflictingTLS reports an injected *tls.Config next to tls file options.
	ErrConflictingTLS = errors.New("nats: injected TLS config and tls file options are mutually exclusive")
	// ErrNoJetStream reports a Client without a JetStream context.
	ErrNoJetStream = errors.New("nats: jetstream context is not initialized")
)

// Config holds NATS connection settings.
type Config struct {
	URL        string        `koanf:"url"`        // e.g. "nats://localhost:4222"
	Name       string        `koanf:"name"`       // client name reported to the server
	Creds      string        `koanf:"creds"`      // JWT + NKey seed .creds file
	NKey       string        `koanf:"nkey"`       // NKey user seed file
	TLS        TLSConfig     `koanf:"tls"`        // PEM files; any set file enables TLS
	AckTimeout time.Duration `koanf:"acktimeout"` // JetStream PubAck wait
}

// TLSConfig names PEM files for the NATS connection. CA alone verifies a
// server with a private CA; Cert and Key add a client certificate.
type TLSConfig struct {
	CA   string `koanf:"ca"`
	Cert string `koanf:"cert"`
	Key  string `koanf:"key"`
}

// Client wraps an open NATS connection and its JetStream context.
type Client struct {
	nc         *nats.Conn
	js         jetstream.JetStream
	logger     *slog.Logger
	ackTimeout time.Duration
}

// Module is the fx module that provides a *Client.
//
//	fx.New(nats.Module)
var Module = fx.Module(
	"golusoris.nats",
	fx.Provide(newFromConfig),
)

type params struct {
	fx.In
	Config *config.Config
	Logger *slog.Logger
	LC     fx.Lifecycle
	TLS    *tls.Config `name:"golusoris.nats.tls" optional:"true"`
}

// ProvideTLSConfig registers constructor, which returns a *tls.Config, as the
// TLS configuration of [Module]; use it for configurations that reload
// certificates. It excludes the nats.tls file options.
func ProvideTLSConfig(constructor any) fx.Option {
	return fx.Provide(fx.Annotate(constructor, fx.ResultTags(`name:"golusoris.nats.tls"`)))
}

func newFromConfig(p params) (*Client, error) {
	var cfg Config
	if err := p.Config.Unmarshal("nats", &cfg); err != nil {
		return nil, fmt.Errorf("nats: config: %w", err)
	}
	if cfg.URL == "" {
		cfg.URL = nats.DefaultURL
	}

	opts, err := connectOptions(cfg, p.TLS, p.Logger)
	if err != nil {
		return nil, err
	}

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats: connect %s: %w", cfg.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}

	c := &Client{nc: nc, js: js, logger: p.Logger, ackTimeout: cfg.AckTimeout}

	p.LC.Append(fx.Hook{
		OnStop: func(_ context.Context) error {
			nc.Close()
			return nil
		},
	})

	return c, nil
}

// Publish publishes a message to the given subject (core NATS, fire-and-forget).
func (c *Client) Publish(subject string, data []byte) error {
	if err := c.nc.Publish(subject, data); err != nil {
		return fmt.Errorf("nats: publish %s: %w", subject, err)
	}
	return nil
}

// PublishSync publishes a core NATS message and waits until the server has
// processed the client buffer. Caller cancellation is honored; a five-second
// deadline is applied when ctx has none.
func (c *Client) PublishSync(ctx context.Context, subject string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("nats: publish %s: %w", subject, err)
	}
	if err := c.Publish(subject, data); err != nil {
		return err
	}
	flushCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		flushCtx, cancel = context.WithTimeout(ctx, defaultPublishFlushTimeout)
	}
	defer cancel()
	if err := c.nc.FlushWithContext(flushCtx); err != nil {
		return fmt.Errorf("nats: flush publish %s: %w", subject, err)
	}
	return nil
}

func connectOptions(cfg Config, injected *tls.Config, logger *slog.Logger) ([]nats.Option, error) {
	opts := []nats.Option{
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			logger.Error("nats error", "err", err)
		}),
	}
	if cfg.Name != "" {
		opts = append(opts, nats.Name(cfg.Name))
	}
	auth, err := authOptions(cfg)
	if err != nil {
		return nil, err
	}
	secure, err := tlsOptions(cfg.TLS, injected)
	if err != nil {
		return nil, err
	}
	return append(append(opts, auth...), secure...), nil
}

func authOptions(cfg Config) ([]nats.Option, error) {
	switch {
	case cfg.Creds != "" && cfg.NKey != "":
		return nil, ErrConflictingAuth
	case cfg.Creds != "":
		// nats.go reads the creds file on every (re)connect, so rotation needs no restart.
		return []nats.Option{nats.UserCredentials(cfg.Creds)}, nil
	case cfg.NKey != "":
		opt, err := nats.NkeyOptionFromSeed(cfg.NKey)
		if err != nil {
			return nil, fmt.Errorf("nats: nkey seed: %w", err)
		}
		return []nats.Option{opt}, nil
	default:
		return nil, nil
	}
}

func tlsOptions(files TLSConfig, injected *tls.Config) ([]nats.Option, error) {
	noFiles := files == TLSConfig{}
	switch {
	case injected != nil && !noFiles:
		return nil, ErrConflictingTLS
	case injected != nil:
		return []nats.Option{nats.Secure(injected.Clone())}, nil
	case noFiles:
		return nil, nil
	}
	tlsCfg, err := tlsx.LoadClientConfig(tlsx.Files{CA: files.CA, Cert: files.Cert, Key: files.Key})
	if err != nil {
		return nil, fmt.Errorf("nats: tls: %w", err)
	}
	// Keep the TLS 1.2 floor NATS servers accept; tlsx alone floors at 1.3.
	tlsCfg.MinVersion = tls.VersionTLS12
	return []nats.Option{nats.Secure(tlsCfg)}, nil
}

// Subscribe subscribes to a subject and delivers messages to fn.
// The returned subscription must be unsubscribed when done.
func (c *Client) Subscribe(subject string, fn func(*nats.Msg)) (*nats.Subscription, error) {
	sub, err := c.nc.Subscribe(subject, fn)
	if err != nil {
		return nil, fmt.Errorf("nats: subscribe %s: %w", subject, err)
	}
	return sub, nil
}

// JetStream returns the JetStream context for durable consumers and streams.
func (c *Client) JetStream() jetstream.JetStream { return c.js }

// Conn returns the underlying *nats.Conn for advanced use.
func (c *Client) Conn() *nats.Conn { return c.nc }
