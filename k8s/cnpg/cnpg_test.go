// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cnpg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/k8s/cnpg"
)

const (
	ns     = "db"
	name   = "pg"
	maxAge = 26 * time.Hour
)

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func ts(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }

func cond(typ, status, reason, msg string, ago time.Duration) map[string]any {
	return map[string]any{
		"type": typ, "status": status, "reason": reason, "message": msg,
		"lastTransitionTime": ts(ago),
	}
}

func cluster(status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": name, "namespace": ns},
	}}
	if status != nil {
		u.Object["status"] = status
	}
	return u
}

func fakeClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{cnpg.ClusterGVR(): "ClusterList"}, objs...)
}

func runCheck(t *testing.T, objs ...runtime.Object) error {
	t.Helper()
	fn, err := cnpg.BackupCheck(fakeClient(objs...), clockwork.NewFakeClockAt(now), ns, name, maxAge)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return fn(ctx)
}

func TestBackupCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status map[string]any
		want   error // nil = healthy
	}{
		{"fresh in-tree backup", map[string]any{"lastSuccessfulBackup": ts(time.Hour)}, nil},
		{"fresh plugin backup via condition", map[string]any{"conditions": []any{
			cond("LastBackupSucceeded", "True", "LastBackupSucceeded", "Backup was successful", time.Hour),
		}}, nil},
		{"age equals maxAge", map[string]any{"lastSuccessfulBackup": ts(maxAge)}, nil},
		{"one second past maxAge", map[string]any{"lastSuccessfulBackup": ts(maxAge + time.Second)}, cnpg.ErrBackupStale},
		{"stale plugin backup", map[string]any{"conditions": []any{
			cond("LastBackupSucceeded", "True", "LastBackupSucceeded", "", 48*time.Hour),
		}}, cnpg.ErrBackupStale},
		{"no status", nil, cnpg.ErrNoBackup},
		{"failed backup", map[string]any{
			"lastSuccessfulBackup": ts(time.Hour),
			"conditions": []any{
				cond("LastBackupSucceeded", "False", "LastBackupFailed", "barman-cloud-backup exited 1", time.Minute),
			},
		}, cnpg.ErrBackupFailed},
		{"archiving failing", map[string]any{
			"lastSuccessfulBackup": ts(time.Hour),
			"conditions": []any{
				cond("ContinuousArchiving", "False", "ContinuousArchivingFailing", "archive_command failed", time.Minute),
			},
		}, cnpg.ErrArchivingFailed},
		{"archiving healthy", map[string]any{
			"lastSuccessfulBackup": ts(time.Hour),
			"conditions": []any{
				cond("ContinuousArchiving", "True", "ContinuousArchivingSuccess", "", time.Minute),
				cond("Ready", "True", "ClusterIsReady", "", time.Minute),
			},
		}, nil},
		{"plugin backup running", map[string]any{"conditions": []any{
			cond("LastBackupSucceeded", "False", "BackupStarted", "New Backup starting up", time.Hour),
		}}, nil},
		{"plugin backup stuck", map[string]any{"conditions": []any{
			cond("LastBackupSucceeded", "False", "BackupStarted", "New Backup starting up", 30*time.Hour),
		}}, cnpg.ErrBackupStale},
		{"running backup keeps in-tree success", map[string]any{
			"lastSuccessfulBackup": ts(30 * time.Hour),
			"conditions": []any{
				cond("LastBackupSucceeded", "False", "BackupStarted", "", time.Minute),
			},
		}, cnpg.ErrBackupStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := runCheck(t, cluster(tc.status))
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestBackupCheck_messagesCarryDetail(t *testing.T) {
	t.Parallel()
	err := runCheck(t, cluster(map[string]any{"conditions": []any{
		cond("LastBackupSucceeded", "False", "LastBackupFailed", "barman-cloud-backup exited 1", time.Minute),
	}}))
	require.ErrorContains(t, err, "barman-cloud-backup exited 1")

	err = runCheck(t, cluster(map[string]any{"lastSuccessfulBackup": ts(30 * time.Hour)}))
	require.ErrorContains(t, err, "30h0m0s old, limit 26h0m0s")
}

func TestBackupCheck_missingCluster(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, runCheck(t), cnpg.ErrClusterNotFound)
}

func TestBackupCheck_malformedStatus(t *testing.T) {
	t.Parallel()
	err := runCheck(t, cluster(map[string]any{"lastSuccessfulBackup": "yesterday"}))
	require.ErrorContains(t, err, "status.lastSuccessfulBackup")

	err = runCheck(t, cluster(map[string]any{"conditions": []any{
		map[string]any{"type": "LastBackupSucceeded", "status": "True", "lastTransitionTime": "soon"},
	}}))
	require.ErrorContains(t, err, "condition LastBackupSucceeded lastTransitionTime")
}

func TestBackupCheck_apiErrorIsWrapped(t *testing.T) {
	t.Parallel()
	dyn := fakeClient(cluster(nil))
	dyn.PrependReactor("get", "clusters", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	fn, err := cnpg.BackupCheck(dyn, clock.NewFake(), ns, name, maxAge)
	require.NoError(t, err)
	err = fn(context.Background())
	require.ErrorContains(t, err, "cnpg: get cluster db/pg: connection refused")
	require.NotErrorIs(t, err, cnpg.ErrClusterNotFound)
}

func TestBackupCheck_validatesArguments(t *testing.T) {
	t.Parallel()
	dyn, clk := fakeClient(), clock.NewFake()
	for label, call := range map[string]func() error{
		"nil client":    func() error { _, err := cnpg.BackupCheck(nil, clk, ns, name, maxAge); return err },
		"nil clock":     func() error { _, err := cnpg.BackupCheck(dyn, nil, ns, name, maxAge); return err },
		"empty ns":      func() error { _, err := cnpg.BackupCheck(dyn, clk, "", name, maxAge); return err },
		"empty cluster": func() error { _, err := cnpg.BackupCheck(dyn, clk, ns, "", maxAge); return err },
		"zero maxAge":   func() error { _, err := cnpg.BackupCheck(dyn, clk, ns, name, 0); return err },
	} {
		require.Error(t, call(), label)
	}
}

func TestClusterGVR(t *testing.T) {
	t.Parallel()
	require.Equal(t, "postgresql.cnpg.io/v1, Resource=clusters", cnpg.ClusterGVR().String())
}
