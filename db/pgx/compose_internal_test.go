package pgx

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
)

type stubTracer struct{}

func (stubTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (stubTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestComposeTracer(t *testing.T) {
	t.Parallel()
	a, b := stubTracer{}, stubTracer{}

	if got := composeTracer(nil, nil); got != nil {
		t.Errorf("no tracers: got %T, want nil", got)
	}
	if got := composeTracer(a, nil); got != pgx.QueryTracer(a) {
		t.Errorf("slow only: got %T, want the slow tracer", got)
	}
	if got := composeTracer(nil, []pgx.QueryTracer{b}); got != pgx.QueryTracer(b) {
		t.Errorf("custom only: got %T, want the single custom tracer", got)
	}
	got := composeTracer(a, []pgx.QueryTracer{b})
	if _, ok := got.(*multitracer.Tracer); !ok {
		t.Errorf("slow + custom: got %T, want *multitracer.Tracer", got)
	}
}
