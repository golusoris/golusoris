// Package k8s elects a single leader via the Kubernetes Lease API
// (client-go). One pod wins the Lease, runs the leader callback; on
// loss/restart another pod takes over.
//
// Config keys (env: APP_LEADER_*):
//
//	leader.enabled        # master switch (default false)
//	leader.namespace      # Lease namespace (default "default")
//	leader.name           # Lease name (required when enabled)
//	leader.identity       # this pod's identity (default POD_NAME → hostname → "unknown")
//	leader.lease.duration # Lease TTL (default 15s)
//	leader.lease.renew    # renew deadline (default 10s)
//	leader.lease.retry    # retry period when not leader (default 2s)
package k8s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/leader"
)

// Options tunes the elector. See package doc for config keys.
type Options struct {
	Enabled   bool         `koanf:"enabled"`
	Namespace string       `koanf:"namespace"`
	Name      string       `koanf:"name"`
	Identity  string       `koanf:"identity"`
	Lease     LeaseOptions `koanf:"lease"`
}

// LeaseOptions tunes the Lease timing.
type LeaseOptions struct {
	Duration time.Duration `koanf:"duration"`
	Renew    time.Duration `koanf:"renew"`
	Retry    time.Duration `koanf:"retry"`
}

// DefaultOptions returns disabled + default namespace + 15s/10s/2s timing
// (matches client-go controller defaults).
func DefaultOptions() Options {
	return Options{
		Namespace: "default",
		Lease: LeaseOptions{
			Duration: 15 * time.Second,
			Renew:    10 * time.Second,
			Retry:    2 * time.Second,
		},
	}
}

// Run elects a leader using a k8s Lease, blocking until ctx is canceled.
func Run(ctx context.Context, k kubernetes.Interface, opts Options, cb leader.Callbacks) error {
	opts = opts.withDefaults()
	if opts.Name == "" {
		return errors.New("leader/k8s: leader.name is required when enabled")
	}
	identity := opts.Identity
	if identity == "" {
		identity = defaultIdentity()
	}

	rl, err := resourcelock.New(
		resourcelock.LeasesResourceLock,
		opts.Namespace,
		opts.Name,
		k.CoreV1(),
		k.CoordinationV1(),
		resourcelock.ResourceLockConfig{Identity: identity},
	)
	if err != nil {
		return fmt.Errorf("leader/k8s: build lock: %w", err)
	}
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            rl,
		LeaseDuration:   opts.Lease.Duration,
		RenewDeadline:   opts.Lease.Renew,
		RetryPeriod:     opts.Lease.Retry,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(c context.Context) {
				if cb.OnStartedLeading != nil {
					cb.OnStartedLeading(c)
				}
			},
			OnStoppedLeading: func() {
				if cb.OnStoppedLeading != nil {
					cb.OnStoppedLeading()
				}
			},
			OnNewLeader: func(id string) {
				if cb.OnNewLeader != nil {
					cb.OnNewLeader(id)
				}
			},
		},
	})
	return nil
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Namespace == "" {
		o.Namespace = d.Namespace
	}
	if o.Lease.Duration == 0 {
		o.Lease.Duration = d.Lease.Duration
	}
	if o.Lease.Renew == 0 {
		o.Lease.Renew = d.Lease.Renew
	}
	if o.Lease.Retry == 0 {
		o.Lease.Retry = d.Lease.Retry
	}
	return o
}

func defaultIdentity() string {
	if v := os.Getenv("POD_NAME"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("leader", &opts); err != nil {
		return Options{}, fmt.Errorf("leader/k8s: load options: %w", err)
	}
	return opts, nil
}

// Module wires the k8s-Lease elector into fx. Requires *rest.Config in
// the graph (from k8s/client). `leader.enabled=false` skips wiring.
func Module(cb leader.Callbacks) fx.Option {
	return fx.Module("golusoris.leader.k8s",
		fx.Provide(loadOptions),
		fx.Invoke(func(lc fx.Lifecycle, opts Options, restCfg *rest.Config, logger *slog.Logger) error {
			if !opts.Enabled {
				return nil
			}
			k, err := kubernetes.NewForConfig(restCfg)
			if err != nil {
				return fmt.Errorf("leader/k8s: kubernetes client: %w", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			lc.Append(fx.Hook{
				OnStart: func(_ context.Context) error {
					go func() {
						defer close(done)
						if runErr := Run(ctx, k, opts, cb); runErr != nil {
							logger.Error("leader/k8s: run failed", slog.String("error", runErr.Error()))
						}
					}()
					return nil
				},
				OnStop: func(_ context.Context) error {
					cancel()
					<-done
					return nil
				},
			})
			return nil
		}),
	)
}
