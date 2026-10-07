// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package redis

import (
	"log/slog"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
	redistest "github.com/golusoris/golusoris/testutil/redis"
)

func readiness(t *testing.T, reg *statuspage.Registry) statuspage.Result {
	t.Helper()
	results := reg.RunTagged(t.Context(), health.TagReadiness)
	require.Len(t, results, 1)
	return results[0]
}

func TestReadinessCheckNilClientIsDown(t *testing.T) {
	t.Parallel()
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(ReadinessCheck(nil, 0, nil))
	res := readiness(t, reg)
	require.Equal(t, ReadinessCheckName, res.Name)
	require.Equal(t, statuspage.StatusDown, res.Status)
}

func TestReadinessCheckRealRedisThenClosed(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	client, err := newClient(Options{Addr: redistest.Addr(t)}, logger)
	require.NoError(t, err)
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(ReadinessCheck(client, time.Second, logger))
	require.Equal(t, statuspage.StatusUp, readiness(t, reg).Status)

	client.Close()
	require.Equal(t, statuspage.StatusDown, readiness(t, reg).Status)
}

func TestReadinessModuleRegistersCheck(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	var reg *statuspage.Registry
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() rueidis.Client { return client }),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(statuspage.NewRegistry),
		ReadinessModule,
		fx.Populate(&reg),
	)
	require.NoError(t, app.Err())
	require.Equal(t, statuspage.StatusUp, readiness(t, reg).Status)
}
