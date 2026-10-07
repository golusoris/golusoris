// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nats_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
	"github.com/golusoris/golusoris/pubsub/nats"
	natstestutil "github.com/golusoris/golusoris/testutil/nats"
)

func readiness(t *testing.T, reg *statuspage.Registry) statuspage.Result {
	t.Helper()
	results := reg.RunTagged(t.Context(), health.TagReadiness)
	require.Len(t, results, 1)
	return results[0]
}

func TestReadinessCheckNilConnIsDown(t *testing.T) {
	t.Parallel()
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(nats.ReadinessCheck(nil, 0, nil))
	res := readiness(t, reg)
	require.Equal(t, nats.ReadinessCheckName, res.Name)
	require.Equal(t, statuspage.StatusDown, res.Status)
}

func TestReadinessCheckRealNATSThenClosed(t *testing.T) {
	t.Parallel()
	nc, err := natsgo.Connect(natstestutil.Start(t))
	require.NoError(t, err)
	var logs bytes.Buffer
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(nats.ReadinessCheck(nc, time.Second, slog.New(slog.NewTextHandler(&logs, nil))))
	require.Equal(t, statuspage.StatusUp, readiness(t, reg).Status)

	nc.Close()
	require.Equal(t, statuspage.StatusDown, readiness(t, reg).Status)
	require.Contains(t, logs.String(), nats.ErrNotConnected.Error())
}

func TestReadinessModuleRegistersCheck(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("nats:\n  url: "+natstestutil.Start(t)+"\n"), 0o600))
	cfg, err := config.New(config.Options{EnvPrefix: "GOLUSORIS_NATS_READINESS_TEST_", Files: []string{cfgPath}})
	require.NoError(t, err)

	var reg *statuspage.Registry
	app := fx.New(
		fx.NopLogger,
		fx.Supply(cfg),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(statuspage.NewRegistry),
		nats.Module,
		nats.ReadinessModule,
		fx.Populate(&reg),
	)
	require.NoError(t, app.Err())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	defer func() { require.NoError(t, app.Stop(ctx)) }()
	require.Equal(t, statuspage.StatusUp, readiness(t, reg).Status)
}
