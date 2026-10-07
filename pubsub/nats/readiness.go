// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

// ReadinessCheckName names the check that [ReadinessModule] registers.
const ReadinessCheckName = "nats"

// ErrNilConn reports a readiness check built without a connection.
var ErrNilConn = errors.New("nats: nil connection")

// ErrNotConnected reports a connection that is not in the CONNECTED state.
var ErrNotConnected = errors.New("nats: not connected")

// ReadinessCheck returns a readiness-tagged check that requires nc to be
// CONNECTED and completes a server round trip (flush) within timeout (<= 0
// uses [health.DefaultDependencyTimeout]). A non-nil logger records failure
// causes; the public message stays generic.
func ReadinessCheck(nc *nats.Conn, timeout time.Duration, logger *slog.Logger) statuspage.Check {
	return health.DependencyCheck(ReadinessCheckName, timeout, logger, func(ctx context.Context) error {
		if nc == nil {
			return ErrNilConn
		}
		if status := nc.Status(); status != nats.CONNECTED {
			return fmt.Errorf("%w: %s", ErrNotConnected, status)
		}
		if err := nc.FlushWithContext(ctx); err != nil {
			return fmt.Errorf("nats: flush: %w", err)
		}
		return nil
	})
}

// ReadinessModule registers [ReadinessCheck] for the graph's client on the
// app's *statuspage.Registry, so /readyz fails while NATS is unreachable.
// Opt-in; requires a *Client ([Module]), *slog.Logger, and a
// *statuspage.Registry.
var ReadinessModule = fx.Module(
	"golusoris.nats.readiness",
	fx.Invoke(registerReadiness),
)

func registerReadiness(reg *statuspage.Registry, client *Client, logger *slog.Logger) {
	reg.Register(ReadinessCheck(client.Conn(), health.DefaultDependencyTimeout, logger))
}
