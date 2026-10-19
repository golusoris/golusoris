// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build envtest

package operator

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/golusoris/golusoris/core/config"
)

func startOperatorEnvtest(t *testing.T) string {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("skipping envtest: KUBEBUILDER_ASSETS is not set")
	}
	env := &envtest.Environment{}
	if _, err := env.Start(); err != nil {
		t.Fatalf("env.Start: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := env.Stop(); stopErr != nil {
			t.Errorf("env.Stop: %v", stopErr)
		}
	})
	user, err := env.AddUser(envtest.User{Name: "golusoris-operator", Groups: []string{"system:masters"}}, nil)
	if err != nil {
		t.Fatalf("env.AddUser: %v", err)
	}
	kcBytes, err := user.KubeConfig()
	if err != nil {
		t.Fatalf("user.KubeConfig: %v", err)
	}
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, kcBytes, 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func newOperatorTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("operator:\n  metrics_addr: \"0\"\n  health_probe_addr: \"0\"\n")
	if err := os.WriteFile(cfgPath, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{cfgPath}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

func testOperatorPositive(t *testing.T, kubeconfigPath string) {
	t.Helper()
	t.Setenv("KUBECONFIG", kubeconfigPath)
	cfg := newOperatorTestConfig(t)

	var mgr manager.Manager
	app := fx.New(
		Module,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Populate(&mgr),
	)

	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStart()
	require.NoError(t, app.Start(startCtx))

	defer func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelStop()
		if err := app.Stop(stopCtx); err != nil {
			t.Errorf("app.Stop: %v", err)
		}
	}()

	getCtx, cancelGet := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelGet()
	var ns corev1.Namespace
	err := mgr.GetAPIReader().Get(getCtx, client.ObjectKey{Name: "default"}, &ns)
	require.NoError(t, err)
	require.Equal(t, "default", ns.Name)
}

func testOperatorNegative(t *testing.T) {
	t.Helper()
	badKubeconfig := `apiVersion: v1
kind: Config
current-context: bad
contexts:
- name: bad
  context:
    cluster: bad
    user: bad
clusters:
- name: bad
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
users:
- name: bad
  user:
    token: dummy
`
	badPath := filepath.Join(t.TempDir(), "bad_kubeconfig")
	if err := os.WriteFile(badPath, []byte(badKubeconfig), 0o600); err != nil {
		t.Fatalf("write bad kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", badPath)
	cfg := newOperatorTestConfig(t)

	var badMgr manager.Manager
	badApp := fx.New(
		Module,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Populate(&badMgr),
	)

	startCtx, cancelStart := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelStart()
	if err := badApp.Start(startCtx); err != nil {
		return
	}

	defer func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelStop()
		if err := badApp.Stop(stopCtx); err != nil {
			t.Logf("badApp.Stop: %v", err)
		}
	}()

	getCtx, cancelGet := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelGet()
	var ns corev1.Namespace
	err := badMgr.GetAPIReader().Get(getCtx, client.ObjectKey{Name: "default"}, &ns)
	if err == nil {
		t.Fatal("expected Get to fail with unreachable server, got nil")
	}
}

//nolint:paralleltest // WHY: t.Setenv mutates process environment.
func TestEnvtest_Operator(t *testing.T) {
	kubeconfigPath := startOperatorEnvtest(t)

	t.Run("positive_start_and_get_default_namespace", func(t *testing.T) {
		testOperatorPositive(t, kubeconfigPath)
	})
	t.Run("negative_bad_server_url_fails", func(t *testing.T) {
		testOperatorNegative(t)
	})
}
