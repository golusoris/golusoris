// Package leader wraps client-go's leader election with golusoris fx wiring.
//
// One pod in a Deployment / StatefulSet wins a Lease and runs the leader
// callback; on loss/restart, another pod takes over. Useful for cron-like
// background work that must not run on every replica.
//
// Apps register a [Callbacks] struct; the module starts the elector during
// fx Start and cancels it on Stop. The Lease is created in the configured
// namespace + name; both must be unique per "thing being elected".
//
// Config keys (env: APP_LEADER_*):
//
//	leader.enabled       # master switch (default false)
//	leader.namespace     # k8s namespace for the Lease (default "default")
//	leader.name          # Lease name (required when enabled)
//	leader.identity      # this pod's identity (default = POD_NAME or hostname)
//	leader.lease.duration # Lease TTL (default 15s)
//	leader.lease.renew   # renew deadline (default 10s)
//	leader.lease.retry   # retry period when not leader (default 2s)
package leader

import (
	"context"
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
)

// Options tunes the elector.
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

// Callbacks fires on lease state changes. OnStartedLeading runs in a fresh
// goroutine when this pod becomes leader and is canceled (via the supplied
// ctx) when the lease is lost. OnStoppedLeading runs after the cancellation
// completes. OnNewLeader fires whenever any pod (including this one) becomes
// leader.
type Callbacks struct {
	OnStartedLeading func(ctx context.Context)
	OnStoppedLeading func()
	OnNewLeader      func(identity string)
}

// Run blocks the calling goroutine running the elector until ctx is canceled.
// Most apps use [Module] instead, which wires Run into the fx lifecycle.
func Run(ctx context.Context, k kubernetes.Interface, opts Options, cb Callbacks) error {
	opts = opts.withDefaults()
	if opts.Name == "" {
		return fmt.Errorf("leader: leader.name is required when enabled")
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
		return fmt.Errorf("leader: build lock: %w", err)
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
		return Options{}, fmt.Errorf("leader: load options: %w", err)
	}
	return opts, nil
}

// Module starts the elector during fx Start (when enabled). The leader
// callback is the user's responsibility to register via fx.Decorate or by
// providing Callbacks.
//
// Requires *rest.Config in the fx graph (provided by k8s/client). Apps not
// running on k8s set leader.enabled=false to skip wiring entirely.
func Module(cb Callbacks) fx.Option {
	return fx.Module("golusoris.k8s.leader",
		fx.Provide(loadOptions),
		fx.Invoke(func(lc fx.Lifecycle, opts Options, restCfg *rest.Config, logger *slog.Logger) error {
			if !opts.Enabled {
				return nil
			}
			k, err := kubernetes.NewForConfig(restCfg)
			if err != nil {
				return fmt.Errorf("leader: kubernetes client: %w", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			lc.Append(fx.Hook{
				OnStart: func(_ context.Context) error {
					go func() {
						defer close(done)
						if runErr := Run(ctx, k, opts, cb); runErr != nil {
							logger.Error("leader: run failed", slog.String("error", runErr.Error()))
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
