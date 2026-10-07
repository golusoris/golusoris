// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golusoris/golusoris/observability/statuspage"
)

// DefaultDependencyTimeout bounds one dependency probe; it stays below the
// registry's 2s per-check timeout so the probe's own deadline fires first.
const DefaultDependencyTimeout = time.Second

// ErrDependencyNotReady is the public failure of a dependency check. The
// probe's cause goes to the log only, so addresses and credentials stay off
// /status and /readyz?verbose=1.
var ErrDependencyNotReady = errors.New("dependency not ready")

// DependencyCheck returns a readiness-tagged check named name that runs probe
// within timeout (<= 0 uses [DefaultDependencyTimeout]). A failing probe
// reports [ErrDependencyNotReady]; a non-nil logger records the cause. A nil
// probe yields a check the registry reports down.
func DependencyCheck(
	name string,
	timeout time.Duration,
	logger *slog.Logger,
	probe func(context.Context) error,
) statuspage.Check {
	check := statuspage.Check{Name: name, Tags: []string{TagReadiness}}
	if probe == nil {
		return check
	}
	if timeout <= 0 {
		timeout = DefaultDependencyTimeout
	}
	check.Fn = func(ctx context.Context) error {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		err := probe(probeCtx)
		if err == nil {
			return nil
		}
		if logger != nil {
			logger.WarnContext(ctx, "k8s/health: dependency not ready",
				slog.String("check", name), slog.String("error", err.Error()))
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%s: probe timed out: %w", name, ErrDependencyNotReady)
		}
		return fmt.Errorf("%s: probe failed: %w", name, ErrDependencyNotReady)
	}
	return check
}
