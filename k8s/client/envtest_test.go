// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build envtest

package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/golusoris/golusoris/k8s/podinfo"
)

func startClientEnvtest(t *testing.T) string {
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
	user, err := env.AddUser(envtest.User{Name: "golusoris", Groups: []string{"system:masters"}}, nil)
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

func testClientPositive(t *testing.T, path string) {
	t.Helper()
	r, err := New(Options{Kubeconfig: path})
	require.NoError(t, err)
	if r.Source != SourceKubeconfig {
		t.Errorf("Source = %q, want %q", r.Source, SourceKubeconfig)
	}
	if r.Path != path {
		t.Errorf("Path = %q, want %q", r.Path, path)
	}
	cs, err := kubernetes.NewForConfig(r.Config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	foundDefault := false
	for _, ns := range list.Items {
		if ns.Name == "default" {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		t.Error("namespaces list does not contain default")
	}
}

func testClientNegative(t *testing.T, path string) {
	t.Helper()
	_, err := New(Options{Kubeconfig: path, Context: "missing"})
	if err == nil {
		t.Error("expected error for missing context, got nil")
	}
}

func testClientBoundary(t *testing.T, path string) {
	t.Helper()
	r, err := New(Options{Kubeconfig: path, QPS: 0, Burst: 0})
	require.NoError(t, err)
	if r.Config.QPS != 20 || r.Config.Burst != 30 {
		t.Errorf("QPS/Burst = %v/%v, want 20/30", r.Config.QPS, r.Config.Burst)
	}
}

//nolint:paralleltest // WHY: t.Setenv mutates process environment.
func TestEnvtest_Client(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	if podinfo.IsInCluster() {
		t.Fatal("podinfo.IsInCluster() must be false for kubeconfig test")
	}
	path := startClientEnvtest(t)

	t.Run("positive_list_namespaces", func(t *testing.T) {
		testClientPositive(t, path)
	})
	t.Run("negative_missing_context", func(t *testing.T) {
		testClientNegative(t, path)
	})
	t.Run("boundary_default_rate_limits", func(t *testing.T) {
		testClientBoundary(t, path)
	})
}
