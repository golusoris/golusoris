// Package nats provides an fx-wired NATS JetStream client via nats-io/nats.go.
//
// Usage:
//
//	fx.New(nats.Module) // requires "nats.*" koanf config
//
// Config keys (koanf prefix "nats"):
//
//	url:  "nats://localhost:4222"
//	name: "my-service"
package nats

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Config holds NATS connection settings.
type Config struct {
	URL  string `koanf:"url"`  // e.g. "nats://localhost:4222"
	Name string `koanf:"name"` // client name reported to the server
}

// Client wraps an open NATS connection and its JetStream context.
type Client struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	logger *slog.Logger
}

// Module is the fx module that provides a *Client.
//
//	fx.New(nats.Module)
var Module = fx.Module("golusoris.nats",
	fx.Provide(newFromConfig),
)

type params struct {
	fx.In
	Config *config.Config
	Logger *slog.Logger
	LC     fx.Lifecycle
}

func newFromConfig(p params) (*Client, error) {
	var cfg Config
	if err := p.Config.Unmarshal("nats", &cfg); err != nil {
		return nil, fmt.Errorf("nats: config: %w", err)
	}
	if cfg.URL == "" {
		cfg.URL = nats.DefaultURL
	}

	opts := []nats.Option{
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			p.Logger.Error("nats error", "err", err)
		}),
	}
	if cfg.Name != "" {
		opts = append(opts, nats.Name(cfg.Name))
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

	c := &Client{nc: nc, js: js, logger: p.Logger}

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
