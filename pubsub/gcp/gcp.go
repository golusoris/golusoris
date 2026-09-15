// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package gcp provides an fx-wired Google Cloud Pub/Sub client via
// cloud.google.com/go/pubsub/v2.
//
// Usage:
//
//	fx.New(gcp.Module) // requires "pubsub.gcp.*" koanf config
//
// Config keys (koanf prefix "pubsub.gcp"):
//
//	project_id: "my-project"
//
// Authentication uses Application Default Credentials. To target the
// Pub/Sub emulator or a [cloud.google.com/go/pubsub/v2/pstest] fake server
// instead of the live service, set the PUBSUB_EMULATOR_HOST environment
// variable (the client library honors it automatically) or construct a
// *pubsub.Client with option.WithGRPCConn yourself and wrap it with
// [ClientFromPubsub].
package gcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	pubsub "cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"go.uber.org/fx"
	"google.golang.org/api/iterator"

	"github.com/golusoris/golusoris/core/config"
)

// ErrClosed is returned by [Client] methods called after [Client.Close].
var ErrClosed = errors.New("gcp: client closed")

// Config holds Google Cloud Pub/Sub connection settings.
type Config struct {
	ProjectID string `koanf:"project_id"` // GCP project ID (required)
}

// Message is an alias for pubsub.Message so callers don't need to import
// cloud.google.com/go/pubsub/v2 directly.
type Message = pubsub.Message

// Client wraps a Google Cloud Pub/Sub client with helpers for publishing
// and subscribing, and caches one [pubsub.Publisher] per topic so callers
// don't have to manage publisher lifecycles themselves.
type Client struct {
	c      *pubsub.Client
	logger *slog.Logger
	closed atomic.Bool

	mu   sync.Mutex
	pubs map[string]*pubsub.Publisher
}

// Module is the fx module that provides a *Client.
//
//	fx.New(gcp.Module)
var Module = fx.Module(
	"golusoris.pubsub.gcp",
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
	if err := p.Config.Unmarshal("pubsub.gcp", &cfg); err != nil {
		return nil, fmt.Errorf("gcp: config: %w", err)
	}
	if cfg.ProjectID == "" {
		return nil, errors.New("gcp: project_id required")
	}

	// Bounded init: a background context is fine here — client construction
	// only sets up auth/transport (no RPC), and fx applies its own start
	// timeout around this provider. Mirrors storage.newBucket's S3 client.
	pc, err := pubsub.NewClient(context.Background(), cfg.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("gcp: new client: %w", err)
	}

	c := ClientFromPubsub(pc)
	c.logger = p.Logger

	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// Ping the service on startup to fail fast on misconfiguration.
			if err := c.Ping(ctx); err != nil {
				return fmt.Errorf("gcp: ping: %w", err)
			}
			return nil
		},
		OnStop: func(_ context.Context) error {
			return c.Close()
		},
	})

	return c, nil
}

// ClientFromPubsub wraps an already-constructed *pubsub.Client. Intended for
// tests and advanced callers that manage the client's lifecycle themselves,
// e.g. a client dialed against a [cloud.google.com/go/pubsub/v2/pstest]
// fake server via option.WithGRPCConn.
func ClientFromPubsub(pc *pubsub.Client) *Client {
	return &Client{c: pc, logger: slog.Default(), pubs: make(map[string]*pubsub.Publisher)}
}

// Publisher returns a cached [pubsub.Publisher] for topicID, creating one on
// first use. The Client owns the publisher's lifecycle — callers must not
// call Stop on it directly; use [Client.Close] instead.
func (c *Client) Publisher(topicID string) *pubsub.Publisher {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pubs[topicID]; ok {
		return p
	}
	p := c.c.Publisher(topicID)
	c.pubs[topicID] = p
	return p
}

// Publish publishes data with the given attributes to topicID and blocks
// until the broker acknowledges it or ctx is done.
func (c *Client) Publish(ctx context.Context, topicID string, data []byte, attrs map[string]string) (string, error) {
	if c.closed.Load() {
		return "", ErrClosed
	}
	result := c.Publisher(topicID).Publish(ctx, &pubsub.Message{Data: data, Attributes: attrs})
	id, err := result.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("gcp: publish to %s: %w", topicID, err)
	}
	return id, nil
}

// Subscriber returns a [pubsub.Subscriber] for subID.
func (c *Client) Subscriber(subID string) *pubsub.Subscriber { return c.c.Subscriber(subID) }

// Subscribe pulls messages from subID and delivers each to fn until ctx is
// cancelled or an unrecoverable error occurs. fn must Ack or Nack every
// message it receives.
func (c *Client) Subscribe(ctx context.Context, subID string, fn func(context.Context, *Message)) error {
	if c.closed.Load() {
		return ErrClosed
	}
	if err := c.Subscriber(subID).Receive(ctx, fn); err != nil {
		return fmt.Errorf("gcp: subscribe %s: %w", subID, err)
	}
	return nil
}

// Ping checks connectivity to the Pub/Sub service by listing at most one
// topic in the configured project.
func (c *Client) Ping(ctx context.Context) error {
	if c.closed.Load() {
		return ErrClosed
	}
	it := c.c.TopicAdminClient.ListTopics(ctx, &pubsubpb.ListTopicsRequest{
		Project:  "projects/" + c.c.Project(),
		PageSize: 1,
	})
	if _, err := it.Next(); err != nil && !errors.Is(err, iterator.Done) {
		return fmt.Errorf("gcp: list topics: %w", err)
	}
	return nil
}

// Close stops every cached publisher and closes the underlying client. It is
// idempotent: calling Close more than once is a no-op after the first call.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.mu.Lock()
	for _, p := range c.pubs {
		p.Stop()
	}
	c.mu.Unlock()
	if err := c.c.Close(); err != nil {
		return fmt.Errorf("gcp: close: %w", err)
	}
	return nil
}

// Pubsub returns the underlying *pubsub.Client for advanced use (topic and
// subscription admin, batching/retry tuning, etc.).
func (c *Client) Pubsub() *pubsub.Client { return c.c }
