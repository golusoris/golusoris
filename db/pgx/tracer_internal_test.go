// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonboulle/clockwork"
)

func TestNewSlowQueryTracer_nonNil(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewRealClock()
	tr := newSlowQueryTracer(slog.Default(), time.Second, clk)
	if tr == nil {
		t.Error("expected non-nil tracer")
	}
}

func TestTraceQueryStart_stashesContext(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	tr := &slowQueryTracer{logger: slog.Default(), threshold: time.Second, clk: clk}
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	if ctx.Value(traceKey{}) == nil {
		t.Error("expected traceData stashed on context, got nil")
	}
}

func TestTraceQueryEnd_belowThreshold(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	var logs bytes.Buffer
	tr := &slowQueryTracer{logger: slog.New(slog.NewTextHandler(&logs, nil)), threshold: time.Hour, clk: clk}
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	clk.Advance(time.Hour - time.Nanosecond)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if logs.Len() != 0 {
		t.Errorf("query below threshold was logged: %s", logs.String())
	}

	ctx = tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 2"})
	clk.Advance(time.Hour)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if !strings.Contains(logs.String(), "slow query") || !strings.Contains(logs.String(), "SELECT 2") {
		t.Errorf("query at threshold was not logged: %q", logs.String())
	}
}

func TestTraceQueryEnd_noStart(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewRealClock()
	tr := &slowQueryTracer{logger: slog.Default(), threshold: time.Second, clk: clk}
	// ctx has no traceData — must not panic
	tr.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{})
}
