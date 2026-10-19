// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build envtest

package cnpg_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/k8s/cnpg"
)

const (
	envtestNS      = "cnpg-envtest"
	envtestCluster = "test-cluster"
	checkMaxAge    = 26 * time.Hour
)

var fixedClockTime = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func setupCNPGEnvtest(t *testing.T) (dynamic.Interface, kubernetes.Interface) {
	t.Helper()
	crdPath := os.Getenv("CNPG_CRD_PATH")
	if os.Getenv("KUBEBUILDER_ASSETS") == "" || crdPath == "" {
		t.Skip("skipping envtest: KUBEBUILDER_ASSETS or CNPG_CRD_PATH is not set")
	}
	env := &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths:              []string{crdPath},
			ErrorIfPathMissing: true,
		},
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("env.Start: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := env.Stop(); stopErr != nil {
			t.Errorf("env.Stop: %v", stopErr)
		}
	})
	dyn, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	return dyn, cs
}

func createNamespaceAndCluster(t *testing.T, cs kubernetes.Interface, dyn dynamic.Interface) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: envtestNS},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	clusterObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "postgresql.cnpg.io/v1",
			"kind":       "Cluster",
			"metadata": map[string]any{
				"name":      envtestCluster,
				"namespace": envtestNS,
			},
			"spec": map[string]any{
				"instances": int64(1),
				"storage": map[string]any{
					"size": "1Gi",
				},
			},
		},
	}
	createCtx, cancelCreate := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCreate()
	_, err = dyn.Resource(cnpg.ClusterGVR()).Namespace(envtestNS).Create(createCtx, clusterObj, metav1.CreateOptions{})
	require.NoError(t, err)
}

func updateStatusAndVerify(t *testing.T, dyn dynamic.Interface, status map[string]any) {
	t.Helper()
	getCtx, cancelGet := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelGet()
	cur, err := dyn.Resource(cnpg.ClusterGVR()).Namespace(envtestNS).Get(getCtx, envtestCluster, metav1.GetOptions{})
	require.NoError(t, err)

	cur.Object["status"] = status
	upCtx, cancelUp := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelUp()
	_, err = dyn.Resource(cnpg.ClusterGVR()).Namespace(envtestNS).UpdateStatus(upCtx, cur, metav1.UpdateOptions{})
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRead()
	readBack, err := dyn.Resource(cnpg.ClusterGVR()).Namespace(envtestNS).Get(readCtx, envtestCluster, metav1.GetOptions{})
	require.NoError(t, err)

	assertStatusPreserved(t, status, readBack.Object)
}

func assertStatusPreserved(t *testing.T, wanted map[string]any, actualObj map[string]any) {
	t.Helper()
	if wantBackup, ok := wanted["lastSuccessfulBackup"].(string); ok {
		gotBackup, found, err := unstructured.NestedString(actualObj, "status", "lastSuccessfulBackup")
		require.NoError(t, err)
		require.True(t, found, "status.lastSuccessfulBackup missing from read back cluster")
		require.Equal(t, wantBackup, gotBackup)
	}
	if wantConds, ok := wanted["conditions"].([]any); ok {
		gotConds, found, err := unstructured.NestedSlice(actualObj, "status", "conditions")
		require.NoError(t, err)
		require.True(t, found, "status.conditions missing from read back cluster")
		require.Len(t, gotConds, len(wantConds))
	}
}

func executeCheck(t *testing.T, dyn dynamic.Interface, clk clock.Clock, cluster string) error {
	t.Helper()
	fn, err := cnpg.BackupCheck(dyn, clk, envtestNS, cluster, checkMaxAge)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return fn(ctx)
}

func testCNPGPositive(t *testing.T, dyn dynamic.Interface, clk clock.Clock) {
	t.Helper()
	status := map[string]any{
		"lastSuccessfulBackup": fixedClockTime.Add(-1 * time.Hour).Format(time.RFC3339),
		"conditions": []any{
			map[string]any{
				"type":               "LastBackupSucceeded",
				"status":             "True",
				"reason":             "LastBackupSucceeded",
				"message":            "Backup succeeded",
				"lastTransitionTime": fixedClockTime.Add(-1 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	updateStatusAndVerify(t, dyn, status)
	err := executeCheck(t, dyn, clk, envtestCluster)
	require.NoError(t, err)
}

func testCNPGNegativeBackup(t *testing.T, dyn dynamic.Interface, clk clock.Clock) {
	t.Helper()
	status := map[string]any{
		"lastSuccessfulBackup": fixedClockTime.Add(-1 * time.Hour).Format(time.RFC3339),
		"conditions": []any{
			map[string]any{
				"type":               "LastBackupSucceeded",
				"status":             "False",
				"reason":             "LastBackupFailed",
				"message":            "barman backup exited 1",
				"lastTransitionTime": fixedClockTime.Add(-5 * time.Minute).Format(time.RFC3339),
			},
		},
	}
	updateStatusAndVerify(t, dyn, status)
	err := executeCheck(t, dyn, clk, envtestCluster)
	require.ErrorIs(t, err, cnpg.ErrBackupFailed)
}

func testCNPGNegativeArchiving(t *testing.T, dyn dynamic.Interface, clk clock.Clock) {
	t.Helper()
	status := map[string]any{
		"lastSuccessfulBackup": fixedClockTime.Add(-1 * time.Hour).Format(time.RFC3339),
		"conditions": []any{
			map[string]any{
				"type":               "ContinuousArchiving",
				"status":             "False",
				"reason":             "ContinuousArchivingFailing",
				"message":            "archive_command failed",
				"lastTransitionTime": fixedClockTime.Add(-5 * time.Minute).Format(time.RFC3339),
			},
		},
	}
	updateStatusAndVerify(t, dyn, status)
	err := executeCheck(t, dyn, clk, envtestCluster)
	require.ErrorIs(t, err, cnpg.ErrArchivingFailed)
}

func testCNPGBoundary(t *testing.T, dyn dynamic.Interface, clk clock.Clock) {
	t.Helper()
	status := map[string]any{
		"lastSuccessfulBackup": fixedClockTime.Add(-checkMaxAge - time.Second).Format(time.RFC3339),
	}
	updateStatusAndVerify(t, dyn, status)
	err := executeCheck(t, dyn, clk, envtestCluster)
	require.ErrorIs(t, err, cnpg.ErrBackupStale)
}

//nolint:paralleltest // WHY: subtests sequentially mutate status of the shared cluster fixture.
func TestEnvtest_CNPGBackupCheck(t *testing.T) {
	dyn, cs := setupCNPGEnvtest(t)
	createNamespaceAndCluster(t, cs, dyn)
	clk := clockwork.NewFakeClockAt(fixedClockTime)

	t.Run("positive_recent_backup_and_condition_true", func(t *testing.T) {
		testCNPGPositive(t, dyn, clk)
	})
	t.Run("negative_backup_failed", func(t *testing.T) {
		testCNPGNegativeBackup(t, dyn, clk)
	})
	t.Run("negative_continuous_archiving_failed", func(t *testing.T) {
		testCNPGNegativeArchiving(t, dyn, clk)
	})
	t.Run("boundary_backup_older_than_max_age", func(t *testing.T) {
		testCNPGBoundary(t, dyn, clk)
	})
	t.Run("negative_missing_cluster", func(t *testing.T) {
		err := executeCheck(t, dyn, clk, "missing-cluster")
		require.ErrorIs(t, err, cnpg.ErrClusterNotFound)
	})
}
