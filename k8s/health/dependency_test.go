// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package health_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

func runCheck(t *testing.T, check statuspage.Check) statuspage.Result {
	t.Helper()
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(check)
	results := reg.RunTagged(t.Context(), health.TagReadiness)
	require.Len(t, results, 1, "dependency checks must be readiness-tagged")
	return results[0]
}

func TestDependencyCheckUp(t *testing.T) {
	t.Parallel()
	res := runCheck(t, health.DependencyCheck("db", time.Second, nil, func(context.Context) error { return nil }))
	require.Equal(t, statuspage.StatusUp, res.Status)
	require.Equal(t, "db", res.Name)
}

func TestDependencyCheckHidesCauseAndLogsIt(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	cause := errors.New("dial postgres://app:hunter2@10.0.0.5")
	res := runCheck(t, health.DependencyCheck("db", time.Second, logger, func(context.Context) error { return cause }))

	require.Equal(t, statuspage.StatusDown, res.Status)
	require.Equal(t, "db: probe failed: "+health.ErrDependencyNotReady.Error(), res.Message)
	require.NotContains(t, res.Message, "hunter2")
	require.Contains(t, logs.String(), "hunter2", "the cause must reach the log")
}

func TestDependencyCheckTimeoutBoundsProbe(t *testing.T) {
	t.Parallel()
	blocked := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	res := runCheck(t, health.DependencyCheck("db", 10*time.Millisecond, nil, blocked))
	require.Equal(t, statuspage.StatusDown, res.Status)
	require.Contains(t, res.Message, "probe timed out")
}

func TestDependencyCheckZeroTimeoutUsesDefault(t *testing.T) {
	t.Parallel()
	var budget time.Duration
	probe := func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		budget = time.Until(deadline)
		return nil
	}
	runCheck(t, health.DependencyCheck("db", 0, nil, probe))
	require.Greater(t, budget, health.DefaultDependencyTimeout/2)
	require.LessOrEqual(t, budget, health.DefaultDependencyTimeout)
}

func TestDependencyCheckNilProbeIsDown(t *testing.T) {
	t.Parallel()
	res := runCheck(t, health.DependencyCheck("db", time.Second, nil, nil))
	require.Equal(t, statuspage.StatusDown, res.Status)
	require.Equal(t, statuspage.ErrNilCheckFunc.Error(), res.Message)
}
