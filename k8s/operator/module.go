package operator

import (
	"fmt"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

func defaultOptions() Options {
	return Options{
		MetricsAddr:      ":8080",
		HealthProbeAddr:  ":8081",
		GracefulShutdown: 30 * time.Second,
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("operator", &opts); err != nil {
		return Options{}, fmt.Errorf("operator: load options: %w", err)
	}
	return opts, nil
}

// Module provides a controller-runtime [manager.Manager] and runs it under the
// fx lifecycle. Requires [golusoris.Core] for config + log. Apps register CRD
// schemes via [ProvideScheme] and reconcilers via fx.Invoke against the Manager.
var Module = fx.Module("golusoris.k8s.operator",
	fx.Provide(loadOptions),
	fx.Provide(newManager),
	fx.Invoke(runManager),
)
