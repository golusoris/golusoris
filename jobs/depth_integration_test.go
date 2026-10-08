// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type depthArgs struct{}

func (depthArgs) Kind() string { return "depth-probe" }

func insertTenantJob(ctx context.Context, t *testing.T, c *jobs.Client, queue, tenant string, at time.Time) {
	t.Helper()
	meta, err := json.Marshal(map[string]string{"tenant": tenant})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Insert(ctx, depthArgs{}, &jobs.InsertOpts{Queue: queue, Metadata: meta, ScheduledAt: at}); err != nil {
		t.Fatalf("Insert(%s/%s): %v", queue, tenant, err)
	}
}

// TestPgxDepthQuerier_groupsByQueueStateTenant runs the depth SQL against a
// real river schema through MetricsModule on a private registry.
func TestPgxDepthQuerier_groupsByQueueStateTenant(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{})
	producer, err := jobs.New(rv.Pool, jobs.Options{Enabled: true, ProducerOnly: true}, nil, discard())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	insertTenantJob(ctx, t, producer, "transcode", "acme", time.Time{})
	insertTenantJob(ctx, t, producer, "transcode", "acme", time.Time{})
	insertTenantJob(ctx, t, producer, "transcode", "globex", time.Time{})
	insertTenantJob(ctx, t, producer, jobs.DefaultQueue, "acme", time.Now().Add(time.Hour))

	reg := prometheus.NewRegistry()
	var collector *jobs.DepthCollector
	opts := jobs.DefaultOptions()
	opts.Metrics.TenantKey = "tenant"
	opts.Metrics.Queues = []string{"transcode"}
	app := fxtest.New(t,
		fx.Supply(rv.Pool, opts, discard(), reg),
		jobs.MetricsModule,
		fx.Populate(&collector),
	)
	app.RequireStart()
	defer app.RequireStop()

	depth, err := collector.QueueDepth(ctx, "transcode")
	if err != nil || depth != 3 {
		t.Fatalf("QueueDepth(transcode) = %d, %v; want 3", depth, err)
	}
	want := `
# HELP river_jobs Jobs per queue and state (and tenant when configured).
# TYPE river_jobs gauge
river_jobs{queue="default",state="scheduled",tenant="acme"} 1
river_jobs{queue="transcode",state="available",tenant="acme"} 2
river_jobs{queue="transcode",state="available",tenant="globex"} 1
`
	if err := promtest.GatherAndCompare(reg, strings.NewReader(want), "river_jobs"); err != nil {
		t.Fatal(err)
	}
	if n, err := promtest.GatherAndCount(reg, "river_queue_oldest_available_age_seconds"); err != nil || n != 2 {
		t.Fatalf("oldest-age series = %d, %v; want default + transcode", n, err)
	}
}

// TestTracing_propagatesTraceContextIntoMetadata proves the otelriver plugin
// is wired: an inserted job carries the caller's traceparent.
func TestTracing_propagatesTraceContextIntoMetadata(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{})
	opts := jobs.Options{Enabled: true, Tracing: jobs.TracingOptions{Enabled: true, Propagate: true}}
	producer, err := jobs.New(rv.Pool, opts, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}, TraceFlags: trace.FlagsSampled})
	ctx, cancel := context.WithTimeout(trace.ContextWithSpanContext(context.Background(), sc), 20*time.Second)
	defer cancel()

	res, err := producer.Insert(ctx, depthArgs{}, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if !strings.Contains(string(res.Job.Metadata), traceID.String()) {
		t.Fatalf("job metadata %s lacks traceparent for %s", res.Job.Metadata, traceID)
	}
}
