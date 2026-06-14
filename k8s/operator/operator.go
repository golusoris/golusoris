// Package operator provides an opt-in fx module wrapping a controller-runtime
// [manager.Manager], so apps can ship Kubernetes CRDs + reconcilers the way
// they ship HTTP handlers.
//
// Register CRD API types via [ProvideScheme] and reconcilers via fx.Invoke
// against the provided [manager.Manager]:
//
//	fx.New(
//	    golusoris.Core,
//	    operator.Module, // provides manager.Manager + runs it on Start
//	    operator.ProvideScheme(myv1.AddToScheme),
//	    fx.Invoke(func(mgr manager.Manager) error {
//	        return (&MyReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr)
//	    }),
//	).Run()
//
// The manager resolves its rest.Config from the ambient environment (in-cluster
// ServiceAccount or a kubeconfig) via [ctrl.GetConfig]. See the
// scaffold-operator skill for the full CRD + reconciler workflow.
package operator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// schemeGroup is the fx value group [SchemeAdder]s are collected from.
const schemeGroup = `group:"operator.scheme"`

// Options tunes the controller-runtime manager. Config keys live under the
// "operator" prefix; the value from defaultOptions is usable as-is.
type Options struct {
	// MetricsAddr binds the Prometheus metrics server ("0" disables it).
	MetricsAddr string `koanf:"metrics_addr"`
	// HealthProbeAddr binds the health/readiness probe endpoints.
	HealthProbeAddr string `koanf:"health_probe_addr"`
	// LeaderElection enables controller-runtime leader election so only one
	// replica reconciles at a time.
	LeaderElection bool `koanf:"leader_election"`
	// LeaderElectionID is the lease name used when LeaderElection is true.
	LeaderElectionID string `koanf:"leader_election_id"`
	// GracefulShutdown bounds how long the manager waits for runnables to stop.
	GracefulShutdown time.Duration `koanf:"graceful_shutdown"`
}

// SchemeAdder registers a group of API types (typically a CRD's AddToScheme)
// into the manager's [runtime.Scheme]. Wire one with [ProvideScheme].
type SchemeAdder func(*runtime.Scheme) error

// ProvideScheme wires a [SchemeAdder] into the manager's scheme so the named
// CRD types are known to the manager and its client.
func ProvideScheme(adder SchemeAdder) fx.Option {
	return fx.Provide(fx.Annotate(
		func() SchemeAdder { return adder },
		fx.ResultTags(schemeGroup),
	))
}

// buildScheme starts from the client-go base scheme and applies each adder.
func buildScheme(adders []SchemeAdder) (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("operator: base scheme: %w", err)
	}
	for _, add := range adders {
		if add == nil {
			continue
		}
		if err := add(scheme); err != nil {
			return nil, fmt.Errorf("operator: scheme adder: %w", err)
		}
	}
	return scheme, nil
}

// managerOptions maps Options + scheme onto controller-runtime's options.
func (o Options) managerOptions(scheme *runtime.Scheme) manager.Options {
	timeout := o.GracefulShutdown
	return manager.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: o.MetricsAddr},
		HealthProbeBindAddress:  o.HealthProbeAddr,
		LeaderElection:          o.LeaderElection,
		LeaderElectionID:        o.LeaderElectionID,
		GracefulShutdownTimeout: &timeout,
	}
}

// managerParams are the fx inputs to newManager.
type managerParams struct {
	fx.In
	Options Options
	Adders  []SchemeAdder `group:"operator.scheme"`
	Logger  *slog.Logger
}

// newManager builds a controller-runtime manager from the resolved rest.Config
// (in-cluster or kubeconfig) with the assembled scheme + default probes.
func newManager(p managerParams) (manager.Manager, error) {
	scheme, err := buildScheme(p.Adders)
	if err != nil {
		return nil, err
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("operator: rest config: %w", err)
	}
	mgr, err := manager.New(cfg, p.Options.managerOptions(scheme))
	if err != nil {
		return nil, fmt.Errorf("operator: new manager: %w", err)
	}
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return nil, fmt.Errorf("operator: healthz: %w", err)
	}
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return nil, fmt.Errorf("operator: readyz: %w", err)
	}
	p.Logger.Debug("operator: manager built",
		slog.String("metrics_addr", p.Options.MetricsAddr),
		slog.Bool("leader_election", p.Options.LeaderElection),
	)
	return mgr, nil
}

// runManager starts the manager on fx Start in its own goroutine (Start blocks
// until the context is cancelled) and stops it on fx Stop. A manager exit
// before shutdown triggers an app-wide shutdown.
func runManager(lc fx.Lifecycle, mgr manager.Manager, logger *slog.Logger, sd fx.Shutdowner) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := mgr.Start(ctx); err != nil {
					logger.Error("operator: manager exited", slog.Any("err", err))
					_ = sd.Shutdown()
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})
}
