// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

// ReadinessCheckName names the check that [ReadinessModule] registers.
const ReadinessCheckName = "redis"

// ErrNilClient reports a readiness check built without a client.
var ErrNilClient = errors.New("cache/redis: nil client")

// ReadinessCheck returns a readiness-tagged check that sends PING within
// timeout (<= 0 uses [health.DefaultDependencyTimeout]). A non-nil logger
// records failure causes; the public message stays generic.
func ReadinessCheck(client rueidis.Client, timeout time.Duration, logger *slog.Logger) statuspage.Check {
	return health.DependencyCheck(ReadinessCheckName, timeout, logger, func(ctx context.Context) error {
		if validate.IsNil(client) {
			return ErrNilClient
		}
		if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
			return fmt.Errorf("cache/redis: ping: %w", err)
		}
		return nil
	})
}

// ReadinessModule registers [ReadinessCheck] for the graph's client on the
// app's *statuspage.Registry, so /readyz fails while Redis is unreachable.
// Opt-in; requires a rueidis.Client ([Module]), *slog.Logger, and a
// *statuspage.Registry.
var ReadinessModule = fx.Module(
	"golusoris.cache.redis.readiness",
	fx.Invoke(registerReadiness),
)

func registerReadiness(reg *statuspage.Registry, client rueidis.Client, logger *slog.Logger) {
	reg.Register(ReadinessCheck(client, health.DefaultDependencyTimeout, logger))
}
