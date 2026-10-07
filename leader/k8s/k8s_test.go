// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package k8s_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/leader"
	leaderk8s "github.com/golusoris/golusoris/leader/k8s"
)

func TestRunRequiresName(t *testing.T) {
	t.Parallel()
	err := leaderk8s.Run(context.Background(), fake.NewClientset(), leaderk8s.Options{Enabled: true}, leader.Callbacks{})
	if err == nil {
		t.Fatal("expected error for missing Name")
	}
	if !strings.Contains(err.Error(), "leader.name is required") {
		t.Errorf("err = %q", err)
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	o := leaderk8s.DefaultOptions()
	if o.Namespace != "default" {
		t.Errorf("Namespace = %q", o.Namespace)
	}
	if o.Lease.Duration != 15*time.Second {
		t.Errorf("Lease.Duration = %v", o.Lease.Duration)
	}
	if o.Lease.Renew != 10*time.Second {
		t.Errorf("Lease.Renew = %v", o.Lease.Renew)
	}
	if o.Lease.Retry != 2*time.Second {
		t.Errorf("Lease.Retry = %v", o.Lease.Retry)
	}
}

func TestRun_statusTracksLease(t *testing.T) {
	t.Parallel()
	st := leader.NewStatus()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	opts := leaderk8s.Options{
		Name:     "sched",
		Identity: "pod-a",
		Lease:    leaderk8s.LeaseOptions{Duration: 2 * time.Second, Renew: time.Second, Retry: 100 * time.Millisecond},
	}
	go func() { done <- leaderk8s.Run(ctx, fake.NewClientset(), opts, st.Observe(leader.Callbacks{})) }()
	require.Eventually(t, st.IsLeader, 10*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	require.False(t, st.IsLeader())
}

func TestNamedModule_coexistsWithModule(t *testing.T) {
	t.Parallel()
	var got struct {
		fx.In
		Sched *leader.Status `name:"sched"`
		GC    *leader.Status `name:"gc"`
	}
	app := fx.New(fx.NopLogger,
		fx.Supply(newConfig(t, "leader:\n  elections:\n    sched:\n      name: sched\n"), slog.New(slog.DiscardHandler), &rest.Config{}),
		leaderk8s.Module(leader.Callbacks{}),
		leaderk8s.NamedModule("sched", leader.Callbacks{}),
		leaderk8s.NamedModule("gc", leader.Callbacks{}),
		fx.Populate(&got),
	)
	require.NoError(t, app.Err())
	require.False(t, got.Sched.IsLeader())
	require.False(t, got.GC.IsLeader())
}

func TestNamedModule_invalidKeyFails(t *testing.T) {
	t.Parallel()
	app := fx.New(fx.NopLogger,
		fx.Supply(newConfig(t, "leader: {}\n"), slog.New(slog.DiscardHandler), &rest.Config{}),
		leaderk8s.NamedModule("", leader.Callbacks{}),
	)
	require.ErrorContains(t, app.Err(), "leader/k8s: invalid election key")
}

func TestNamedModule_enabledWithoutNameFailsStartup(t *testing.T) {
	t.Parallel()
	var logs strings.Builder
	app := fx.New(fx.NopLogger,
		fx.Supply(newConfig(t, "leader:\n  elections:\n    sched:\n      enabled: true\n"),
			slog.New(slog.NewTextHandler(&logs, nil)), &rest.Config{Host: "https://127.0.0.1:1"}),
		leaderk8s.NamedModule("sched", leader.Callbacks{}),
	)
	require.NoError(t, app.Err())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	require.NoError(t, app.Stop(ctx))
	require.Contains(t, logs.String(), "leader/k8s[sched]: run failed")
	require.Contains(t, logs.String(), "leader.name is required")
}

func newConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	return cfg
}
