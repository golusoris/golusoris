// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package cnpg reads CloudNativePG (CNPG) Cluster status through the
// dynamic client, without importing the CNPG module, and turns backup
// health into a statuspage check.
//
// Field names follow CNPG v1.30.1 api/v1/cluster_types.go and
// cluster_conditions.go: status.lastSuccessfulBackup (RFC3339, set only for
// in-tree backup methods; deprecated and unset for backup plugins such as
// Barman Cloud) and status.conditions of type LastBackupSucceeded (reasons
// LastBackupSucceeded, LastBackupFailed, BackupStarted) and
// ContinuousArchiving. Plugin backups still set the LastBackupSucceeded
// condition, so its lastTransitionTime dates the last success when the
// deprecated field is absent.
package cnpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/observability/statuspage"
)

// getTimeout bounds one Cluster read when the caller's deadline is later.
const getTimeout = 5 * time.Second

// CNPG condition types and reasons (api/v1/cluster_types.go).
const (
	conditionBackup     = "LastBackupSucceeded"
	conditionArchiving  = "ContinuousArchiving"
	reasonBackupFailed  = "LastBackupFailed"
	reasonBackupStarted = "BackupStarted"
)

var (
	// ErrClusterNotFound reports a missing Cluster object.
	ErrClusterNotFound = errors.New("cnpg: cluster not found")
	// ErrNoBackup reports a Cluster without any successful backup.
	ErrNoBackup = errors.New("cnpg: no successful backup recorded")
	// ErrBackupFailed reports a LastBackupSucceeded condition with reason
	// LastBackupFailed.
	ErrBackupFailed = errors.New("cnpg: last backup failed")
	// ErrBackupStale reports a last successful backup older than maxAge.
	ErrBackupStale = errors.New("cnpg: last successful backup is too old")
	// ErrArchivingFailed reports a False ContinuousArchiving condition: WAL
	// archiving, and with it point-in-time recovery, is broken.
	ErrArchivingFailed = errors.New("cnpg: continuous WAL archiving is failing")
)

// ClusterGVR is the CNPG Cluster resource, postgresql.cnpg.io/v1 clusters.
func ClusterGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}
}

// BackupCheck returns a check that fails when the Cluster namespace/cluster
// is missing, its WAL archiving or last backup failed, or its last
// successful backup is older than maxAge. A backup in progress passes while
// it started within maxAge. Register it untagged to report on /status
// without failing readiness.
func BackupCheck(dyn dynamic.Interface, clk clock.Clock, namespace, cluster string, maxAge time.Duration) (statuspage.CheckFunc, error) {
	switch {
	case dyn == nil || clk == nil:
		return nil, errors.New("cnpg: dynamic client and clock are required")
	case namespace == "" || cluster == "":
		return nil, fmt.Errorf("cnpg: namespace %q and cluster %q must be set", namespace, cluster)
	case maxAge <= 0:
		return nil, fmt.Errorf("cnpg: maxAge %v must be positive", maxAge)
	}
	res := dyn.Resource(ClusterGVR()).Namespace(namespace)
	return func(ctx context.Context) error {
		getCtx, cancel := context.WithTimeout(ctx, getTimeout)
		defer cancel()
		obj, err := res.Get(getCtx, cluster, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %s/%s", ErrClusterNotFound, namespace, cluster)
		}
		if err != nil {
			return fmt.Errorf("cnpg: get cluster %s/%s: %w", namespace, cluster, err)
		}
		st, err := readStatus(obj)
		if err != nil {
			return fmt.Errorf("cnpg: cluster %s/%s: %w", namespace, cluster, err)
		}
		return st.evaluate(clk.Now(), maxAge)
	}, nil
}

// condition is the subset of metav1.Condition the check reads.
type condition struct {
	status, reason, message string
	since                   time.Time
}

type backupStatus struct {
	lastSuccess time.Time
	backup      *condition
	archiving   *condition
}

func readStatus(obj *unstructured.Unstructured) (backupStatus, error) {
	var st backupStatus
	raw, _, err := unstructured.NestedString(obj.Object, "status", "lastSuccessfulBackup")
	if err != nil {
		return st, fmt.Errorf("status.lastSuccessfulBackup: %w", err)
	}
	if raw != "" {
		if st.lastSuccess, err = time.Parse(time.RFC3339, raw); err != nil {
			return st, fmt.Errorf("status.lastSuccessfulBackup: %w", err)
		}
	}
	conds, _, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil {
		return st, fmt.Errorf("status.conditions: %w", err)
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		parsed, kind, parseErr := parseCondition(m)
		if parseErr != nil {
			return st, parseErr
		}
		switch kind {
		case conditionBackup:
			st.backup = &parsed
		case conditionArchiving:
			st.archiving = &parsed
		}
	}
	return st, nil
}

func parseCondition(m map[string]any) (condition, string, error) {
	str := func(key string) string {
		v, _ := m[key].(string)
		return v
	}
	c := condition{status: str("status"), reason: str("reason"), message: str("message")}
	if ts := str("lastTransitionTime"); ts != "" {
		since, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return condition{}, "", fmt.Errorf("condition %s lastTransitionTime: %w", str("type"), err)
		}
		c.since = since
	}
	return c, str("type"), nil
}

func (st backupStatus) evaluate(now time.Time, maxAge time.Duration) error {
	if err := st.failure(); err != nil {
		return err
	}
	last, running := st.lastSuccessOrStart()
	if last.IsZero() {
		return ErrNoBackup
	}
	age := now.Sub(last)
	switch {
	case age <= maxAge:
		return nil
	case running:
		return fmt.Errorf("%w: backup running for %v, limit %v", ErrBackupStale, age.Round(time.Second), maxAge)
	default:
		return fmt.Errorf("%w: %v old, limit %v", ErrBackupStale, age.Round(time.Second), maxAge)
	}
}

// failure reports a failing WAL archiver or a failed last backup.
func (st backupStatus) failure() error {
	if st.archiving != nil && st.archiving.status == string(metav1.ConditionFalse) {
		return fmt.Errorf("%w: %s", ErrArchivingFailed, st.archiving.message)
	}
	if st.backup != nil && st.backup.status == string(metav1.ConditionFalse) && st.backup.reason == reasonBackupFailed {
		return fmt.Errorf("%w: %s", ErrBackupFailed, st.backup.message)
	}
	return nil
}

// lastSuccessOrStart dates the last success; without one, a running
// backup's start time stands in (running=true) because plugin backups
// leave no earlier success timestamp in the Cluster status.
func (st backupStatus) lastSuccessOrStart() (time.Time, bool) {
	if !st.lastSuccess.IsZero() || st.backup == nil {
		return st.lastSuccess, false
	}
	switch {
	case st.backup.status == string(metav1.ConditionTrue):
		return st.backup.since, false
	case st.backup.reason == reasonBackupStarted:
		return st.backup.since, true
	}
	return time.Time{}, false
}
