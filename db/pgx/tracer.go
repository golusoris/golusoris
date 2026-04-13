package pgx

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/golusoris/golusoris/clock"
)

// slowQueryTracer is a pgx.QueryTracer that logs queries slower than threshold
// at Warn level. Zero threshold disables tracing at construction time in
// [New], so this tracer assumes threshold > 0.
type slowQueryTracer struct {
	logger    *slog.Logger
	threshold time.Duration
	clk       clock.Clock
}

type traceKey struct{}

type traceData struct {
	start time.Time
	sql   string
}

func newSlowQueryTracer(logger *slog.Logger, threshold time.Duration, clk clock.Clock) pgx.QueryTracer {
	return &slowQueryTracer{logger: logger, threshold: threshold, clk: clk}
}

// TraceQueryStart stashes the start time + SQL on the ctx for TraceQueryEnd.
func (t *slowQueryTracer) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	return context.WithValue(ctx, traceKey{}, traceData{start: t.clk.Now(), sql: data.SQL})
}

// TraceQueryEnd emits a Warn log if the elapsed exceeds threshold.
func (t *slowQueryTracer) TraceQueryEnd(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData,
) {
	v, ok := ctx.Value(traceKey{}).(traceData)
	if !ok {
		return
	}
	elapsed := t.clk.Since(v.start)
	if elapsed < t.threshold {
		return
	}
	attrs := []slog.Attr{
		slog.Duration("elapsed", elapsed),
		slog.Duration("threshold", t.threshold),
		slog.String("sql", v.sql),
	}
	if data.Err != nil {
		attrs = append(attrs, slog.String("error", data.Err.Error()))
	}
	t.logger.LogAttrs(ctx, slog.LevelWarn, "db/pgx: slow query", attrs...)
}
