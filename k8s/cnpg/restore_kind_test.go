// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build cnpgkind

package cnpg_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/k8s/cnpg"
)

func setupKind(t *testing.T) (dynamic.Interface, clock.Clock, string, string, string) {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("skipping kind test: KUBECONFIG is not set")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	require.NoError(t, err)

	dyn, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)

	ns := os.Getenv("CNPG_TEST_NAMESPACE")
	if ns == "" {
		ns = "cnpg-restore"
	}
	source := os.Getenv("CNPG_SOURCE_CLUSTER")
	if source == "" {
		source = "pg-source"
	}
	recovery := os.Getenv("CNPG_RECOVERY_CLUSTER")
	if recovery == "" {
		recovery = "pg-recovery"
	}

	clk := clockwork.NewRealClock()
	return dyn, clk, ns, source, recovery
}

func TestKind_BackupCheck_Positive(t *testing.T) {
	t.Parallel()
	dyn, clk, ns, source, _ := setupKind(t)

	fn, err := cnpg.BackupCheck(dyn, clk, ns, source, 1*time.Hour)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, fn(ctx))
}

func TestKind_BackupCheck_Negative(t *testing.T) {
	t.Parallel()
	dyn, clk, ns, _, _ := setupKind(t)

	fn, err := cnpg.BackupCheck(dyn, clk, ns, "non-existent-cluster", 1*time.Hour)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = fn(ctx)
	require.ErrorIs(t, err, cnpg.ErrClusterNotFound)
}

func TestKind_BackupCheck_Boundary(t *testing.T) {
	t.Parallel()
	dyn, clk, ns, _, recovery := setupKind(t)

	fn, err := cnpg.BackupCheck(dyn, clk, ns, recovery, 1*time.Hour)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = fn(ctx)
	require.ErrorIs(t, err, cnpg.ErrNoBackup)
}
