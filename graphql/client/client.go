// Package client provides a thin fx-wired wrapper around the genqlient
// GraphQL client for consuming external GraphQL APIs.
//
// # Code generation
//
// genqlient generates typed Go functions from .graphql query files plus a
// schema.  Add a genqlient.yaml at the consuming app's root:
//
//	schema: https://api.example.com/graphql
//	queries:
//	  - queries/*.graphql
//	generated: generated/graphql.go
//
// Then run:
//
//	go run github.com/Khan/genqlient
//
// The generated code calls [graphql.Client] which this package provides.
//
// # Usage
//
//	fx.New(
//	    client.Module,
//	    fx.Invoke(func(c graphql.Client) {
//	        // call generated typed functions, e.g.:
//	        // resp, err := generated.GetUser(ctx, c, userID)
//	    }),
//	)
//
// Config keys (env: APP_GRAPHQL_CLIENT_*):
//
//	graphql.client.endpoint       # required — e.g. https://api.example.com/graphql
//	graphql.client.timeout        # per-request timeout (default: 30s)
//	graphql.client.bearer_token   # added as Authorization: Bearer <token>
//	graphql.client.api_key        # added as X-Api-Key header
//	graphql.client.websocket      # use WebSocket transport for subscriptions (default: false)
package client

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Khan/genqlient/graphql"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

const defaultTimeout = 30 * time.Second

// Config holds the GraphQL client configuration.
type Config struct {
	// Endpoint is the full GraphQL API URL (required).
	Endpoint string `koanf:"endpoint"`
	// Timeout is the per-request HTTP timeout (default: 30s).
	Timeout time.Duration `koanf:"timeout"`
	// BearerToken is added as Authorization: Bearer <token> if non-empty.
	BearerToken string `koanf:"bearer_token"`
	// APIKey is added as X-Api-Key if non-empty.
	APIKey string `koanf:"api_key"`
	// WebSocket enables WebSocket transport for subscription support.
	WebSocket bool `koanf:"websocket"`
}

// DefaultConfig returns safe defaults. Endpoint must be set by the app.
func DefaultConfig() Config {
	return Config{Timeout: defaultTimeout}
}

func (c Config) withDefaults() Config {
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	return c
}

// Module provides a genqlient [graphql.Client] into the fx graph.
// Requires *config.Config.
var Module = fx.Module("golusoris.graphql.client",
	fx.Provide(loadConfig),
	fx.Provide(newClient),
)

func loadConfig(cfg *config.Config) (Config, error) {
	c := Config{}
	if err := cfg.Unmarshal("graphql.client", &c); err != nil {
		return Config{}, fmt.Errorf("graphql/client: load config: %w", err)
	}
	return c.withDefaults(), nil
}

func newClient(cfg Config) (graphql.Client, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("graphql/client: endpoint is required")
	}

	transport := authTransport{
		base:        http.DefaultTransport,
		bearerToken: cfg.BearerToken,
		apiKey:      cfg.APIKey,
	}
	hc := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}

	return graphql.NewClient(cfg.Endpoint, hc), nil
}

// authTransport injects auth headers into each request.
type authTransport struct {
	base        http.RoundTripper
	bearerToken string
	apiKey      string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone to avoid mutating the original.
	r := req.Clone(req.Context())
	if t.bearerToken != "" {
		r.Header.Set("Authorization", "Bearer "+t.bearerToken)
	}
	if t.apiKey != "" {
		r.Header.Set("X-Api-Key", t.apiKey)
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, fmt.Errorf("graphql: transport: %w", err)
	}
	return resp, nil
}
