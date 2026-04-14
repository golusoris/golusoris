// Package workflow wraps the Temporal Go SDK as an fx module.
//
// It provides a [client.Client] and (optionally) a [worker.Worker] that are
// lifecycle-managed by fx: the client is closed on fx Stop; the worker is
// started on fx Start and stopped gracefully on fx Stop.
//
// Config keys (env: APP_WORKFLOW_*):
//
//	workflow.host         # Temporal server host:port (default: localhost:7233)
//	workflow.namespace    # Temporal namespace (default: "default")
//	workflow.task_queue   # task queue name; worker is only started when non-empty
//	workflow.tls          # enable TLS (required for Temporal Cloud)
//	workflow.api_key      # API key for Temporal Cloud (passed as bearer header)
//	workflow.identity     # worker identity label shown in Temporal UI
//
// Usage:
//
//	// In main (app):
//	fx.New(
//	    workflow.Module,
//	    fx.Invoke(func(w worker.Worker) {
//	        w.RegisterWorkflow(MyWorkflow)
//	        w.RegisterActivity(MyActivity{})
//	    }),
//	)
//
//	// Enqueue from anywhere with the injected client:
//	fx.Invoke(func(c client.Client) {
//	    run, err := c.ExecuteWorkflow(ctx, options, MyWorkflow, args...)
//	})
package workflow

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Config holds connection options for a Temporal server or Temporal Cloud.
type Config struct {
	// Host is the Temporal server gRPC endpoint (default: localhost:7233).
	Host string `koanf:"host"`
	// Namespace is the Temporal namespace (default: "default").
	Namespace string `koanf:"namespace"`
	// TaskQueue is the task queue name. A worker is only started when
	// non-empty; omit for producer-only services.
	TaskQueue string `koanf:"task_queue"`
	// TLS enables TLS (required for Temporal Cloud).
	TLS bool `koanf:"tls"`
	// APIKey is used for Temporal Cloud API-key auth (bearer header).
	APIKey string `koanf:"api_key"`
	// Identity is the worker label shown in the Temporal Web UI.
	// Defaults to hostname if empty.
	Identity string `koanf:"identity"`
}

// DefaultConfig returns safe defaults targeting a local Temporal server.
func DefaultConfig() Config {
	return Config{
		Host:      "localhost:7233",
		Namespace: "default",
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Host == "" {
		c.Host = d.Host
	}
	if c.Namespace == "" {
		c.Namespace = d.Namespace
	}
	return c
}

// Module wires a Temporal client (and optional worker) into fx.
// Requires *config.Config and *slog.Logger in the graph.
var Module = fx.Module("golusoris.workflow",
	fx.Provide(loadConfig),
	fx.Provide(newClient),
	fx.Provide(newWorker),
)

// Client is an alias so apps import only this package for the type.
type Client = client.Client

// Worker is an alias so apps import only this package for the type.
type Worker = worker.Worker

func loadConfig(cfg *config.Config) (Config, error) {
	opts := DefaultConfig()
	if err := cfg.Unmarshal("workflow", &opts); err != nil {
		return Config{}, fmt.Errorf("workflow: load config: %w", err)
	}
	return opts, nil
}

func newClient(lc fx.Lifecycle, cfg Config, logger *slog.Logger) (client.Client, error) {
	cfg = cfg.withDefaults()

	opts := client.Options{
		HostPort:  cfg.Host,
		Namespace: cfg.Namespace,
		Logger:    log.NewStructuredLogger(logger),
	}
	if cfg.Identity != "" {
		opts.Identity = cfg.Identity
	}
	if cfg.TLS {
		opts.ConnectionOptions = client.ConnectionOptions{
			TLS: &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}
	if cfg.APIKey != "" {
		if !cfg.TLS {
			return nil, errors.New("workflow: TLS must be enabled when using an API key")
		}
		opts.Credentials = client.NewAPIKeyStaticCredentials(cfg.APIKey)
	}

	c, err := client.Dial(opts)
	if err != nil {
		return nil, fmt.Errorf("workflow: dial: %w", err)
	}

	lc.Append(fx.Hook{
		OnStop: func(_ context.Context) error {
			c.Close()
			return nil
		},
	})
	return c, nil
}

func newWorker(lc fx.Lifecycle, cfg Config, c client.Client) worker.Worker {
	if cfg.TaskQueue == "" {
		// No task queue configured → producer-only mode. Return a no-op worker
		// so fx.Provide doesn't fail; callers that inject worker.Worker should
		// guard on cfg.TaskQueue != "" in their own fx.Invoke.
		return worker.New(c, "", worker.Options{DisableRegistrationAliasing: true})
	}

	w := worker.New(c, cfg.TaskQueue, worker.Options{})
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := w.Start(); err != nil {
				return fmt.Errorf("workflow: worker start: %w", err)
			}
			return nil
		},
		OnStop: func(_ context.Context) error {
			w.Stop()
			return nil
		},
	})
	return w
}

