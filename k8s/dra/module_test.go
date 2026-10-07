// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dra_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/k8s/dra"
	"github.com/golusoris/golusoris/k8s/podinfo"
)

func newConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	return cfg
}

func TestModule_publishesOnStartAndCleansUpOnStop(t *testing.T) {
	t.Parallel()
	k := newClient()
	cfg := newConfig(t, "k8s:\n  dra:\n    enabled: true\n    driver: "+driver+"\n")
	var pub *dra.Publisher
	app := fx.New(
		fx.NopLogger,
		fx.Supply(cfg, slog.New(slog.DiscardHandler), podinfo.PodInfo{NodeName: node}),
		fx.Provide(func() kubernetes.Interface { return k }),
		dra.Module(func(context.Context) ([]dra.Device, error) {
			return []dra.Device{gpu(0, "cuda")}, nil
		}),
		fx.Populate(&pub),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(startCtx(t)))
	require.Eventually(t, func() bool { return len(ownSlices(t, k)) == 1 }, 10*time.Second, 20*time.Millisecond)

	require.NoError(t, pub.Update([]dra.Device{gpu(0, "cuda"), gpu(1, "cuda")}))
	require.Eventually(t, func() bool {
		s := ownSlices(t, k)
		return len(s) == 1 && len(s[0].Spec.Devices) == 2
	}, 10*time.Second, 20*time.Millisecond)

	require.NoError(t, app.Stop(startCtx(t)))
	require.Empty(t, ownSlices(t, k))
}

func TestModule_disabledNeedsNoClient(t *testing.T) {
	t.Parallel()
	var pub *dra.Publisher
	app := fx.New(
		fx.NopLogger,
		fx.Supply(newConfig(t, "k8s: {}\n"), slog.New(slog.DiscardHandler)),
		dra.Module(nil),
		fx.Populate(&pub),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(startCtx(t)))
	require.NoError(t, pub.Update([]dra.Device{{Name: "d"}}))
	require.ErrorIs(t, pub.Update([]dra.Device{{Name: "Bad_Name"}}), dra.ErrInvalidDevice)
	require.NoError(t, app.Stop(startCtx(t)))
}

func TestModule_enabledWithoutClientFails(t *testing.T) {
	t.Parallel()
	app := fx.New(
		fx.NopLogger,
		fx.Supply(newConfig(t, "k8s:\n  dra:\n    enabled: true\n    driver: "+driver+"\n    node: "+node+"\n"),
			slog.New(slog.DiscardHandler)),
		dra.Module(nil),
	)
	require.ErrorContains(t, app.Err(), "kubernetes client and logger are required")
}

func TestModule_enabledWithoutNodeFails(t *testing.T) {
	t.Parallel()
	k := newClient()
	app := fx.New(
		fx.NopLogger,
		fx.Supply(newConfig(t, "k8s:\n  dra:\n    enabled: true\n    driver: "+driver+"\n"), slog.New(slog.DiscardHandler)),
		fx.Provide(func() kubernetes.Interface { return k }),
		dra.Module(nil),
	)
	require.ErrorContains(t, app.Err(), `dra: node ""`)
}

func TestModule_sourceErrorFailsStart(t *testing.T) {
	t.Parallel()
	k := newClient()
	app := fx.New(
		fx.NopLogger,
		fx.Supply(newConfig(t, "k8s:\n  dra:\n    enabled: true\n    driver: "+driver+"\n    node: "+node+"\n"),
			slog.New(slog.DiscardHandler)),
		fx.Provide(func() kubernetes.Interface { return k }),
		dra.Module(func(context.Context) ([]dra.Device, error) { return nil, errors.New("probe failed") }),
	)
	require.NoError(t, app.Err())
	require.ErrorContains(t, app.Start(startCtx(t)), "dra: source: probe failed")
	require.Empty(t, ownSlices(t, k))
}
