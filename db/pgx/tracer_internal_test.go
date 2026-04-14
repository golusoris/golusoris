package pgx

import (
	"context"
	"log/slog"
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
	tr := &slowQueryTracer{logger: slog.Default(), threshold: time.Hour, clk: clk}
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	// elapsed is ~0, well below 1h threshold — must not panic
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
}

func TestTraceQueryEnd_noStart(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewRealClock()
	tr := &slowQueryTracer{logger: slog.Default(), threshold: time.Second, clk: clk}
	// ctx has no traceData — must not panic
	tr.TraceQueryEnd(context.Background(), nil, pgx.TraceQueryEndData{})
}
