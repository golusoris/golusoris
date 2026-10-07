// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/golusoris/golusoris/core/clock"
)

type fakeQuerier struct {
	rows     []StateCount
	err      error
	calls    atomic.Int32
	deadline time.Duration
	tenant   string
}

func (f *fakeQuerier) QueryDepth(ctx context.Context, tenantKey string) ([]StateCount, error) {
	f.calls.Add(1)
	f.tenant = tenantKey
	if dl, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(dl)
	}
	return f.rows, f.err
}

func newTestCollector(t *testing.T, q DepthQuerier, opts MetricsOptions) (*DepthCollector, *clockwork.FakeClock) {
	t.Helper()
	clk := clock.NewFake()
	c, err := NewDepthCollector(q, opts, clk, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewDepthCollector: %v", err)
	}
	return c, clk
}

func TestDepthCollector_CollectsBoundedTenantLabels(t *testing.T) {
	t.Parallel()
	q := &fakeQuerier{rows: []StateCount{
		{Queue: "default", State: "available", Tenant: "a", Count: 5, OldestAge: 30 * time.Second},
		{Queue: "default", State: "available", Tenant: "b", Count: 3, OldestAge: 90 * time.Second},
		{Queue: "default", State: "available", Tenant: "c", Count: 1, OldestAge: 10 * time.Second},
		{Queue: "default", State: "running", Tenant: "", Count: 2},
	}}
	c, _ := newTestCollector(t, q, MetricsOptions{TenantKey: "tenant", TenantTopN: 2, Queues: []string{"critical"}})
	want := `
# HELP river_jobs Jobs per queue and state (and tenant when configured).
# TYPE river_jobs gauge
river_jobs{queue="default",state="available",tenant="a"} 5
river_jobs{queue="default",state="available",tenant="b"} 3
river_jobs{queue="default",state="available",tenant="other"} 1
river_jobs{queue="default",state="running",tenant="other"} 2
# HELP river_queue_available Jobs ready to run, per queue.
# TYPE river_queue_available gauge
river_queue_available{queue="critical"} 0
river_queue_available{queue="default"} 9
# HELP river_queue_oldest_available_age_seconds Age of the oldest available job, per queue.
# TYPE river_queue_oldest_available_age_seconds gauge
river_queue_oldest_available_age_seconds{queue="critical"} 0
river_queue_oldest_available_age_seconds{queue="default"} 90
# HELP river_depth_collector_up 1 when the last depth query succeeded.
# TYPE river_depth_collector_up gauge
river_depth_collector_up 1
`
	if err := promtest.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
	if q.tenant != "tenant" {
		t.Fatalf("querier tenant key = %q, want tenant", q.tenant)
	}
}

func TestDepthCollector_FoldsQueuesBeyondMax(t *testing.T) {
	t.Parallel()
	q := &fakeQuerier{rows: []StateCount{
		{Queue: "tiny-gpu", State: "available", Count: 7},
		{Queue: "tiny-cpu", State: "available", Count: 4},
		{Queue: "tiny-npu", State: "available", Count: 1},
	}}
	c, _ := newTestCollector(t, q, MetricsOptions{MaxQueues: 2})
	want := `
# HELP river_queue_available Jobs ready to run, per queue.
# TYPE river_queue_available gauge
river_queue_available{queue="default"} 0
river_queue_available{queue="other"} 5
river_queue_available{queue="tiny-gpu"} 7
`
	if err := promtest.CollectAndCompare(c, strings.NewReader(want), "river_queue_available"); err != nil {
		t.Fatal(err)
	}
}

func TestDepthCollector_CachesWithinTTL(t *testing.T) {
	t.Parallel()
	q := &fakeQuerier{}
	c, clk := newTestCollector(t, q, MetricsOptions{CacheTTL: 10 * time.Second, QueryTimeout: time.Second})
	ctx := context.Background()
	for range 3 {
		if _, err := c.QueueDepth(ctx, DefaultQueue); err != nil {
			t.Fatalf("QueueDepth: %v", err)
		}
	}
	if got := q.calls.Load(); got != 1 {
		t.Fatalf("queries within TTL = %d, want 1", got)
	}
	if q.deadline <= 0 || q.deadline > time.Second {
		t.Fatalf("query deadline = %v, want bounded by 1s", q.deadline)
	}
	clk.Advance(10 * time.Second)
	if _, err := c.QueueDepth(ctx, DefaultQueue); err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if got := q.calls.Load(); got != 2 {
		t.Fatalf("queries after TTL = %d, want 2", got)
	}
}

func TestDepthCollector_QueryErrorServesStaleAndReportsDown(t *testing.T) {
	t.Parallel()
	q := &fakeQuerier{rows: []StateCount{{Queue: "default", State: "available", Count: 4}}}
	c, clk := newTestCollector(t, q, MetricsOptions{CacheTTL: time.Second})
	if _, err := c.QueueDepth(context.Background(), DefaultQueue); err != nil {
		t.Fatalf("warm QueueDepth: %v", err)
	}
	q.err = errors.New("db down")
	clk.Advance(2 * time.Second)
	want := `
# HELP river_depth_collector_up 1 when the last depth query succeeded.
# TYPE river_depth_collector_up gauge
river_depth_collector_up 0
# HELP river_queue_available Jobs ready to run, per queue.
# TYPE river_queue_available gauge
river_queue_available{queue="default"} 4
`
	if err := promtest.CollectAndCompare(c, strings.NewReader(want), "river_depth_collector_up", "river_queue_available"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.QueueDepth(context.Background(), DefaultQueue); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("QueueDepth error = %v, want query failure", err)
	}
}

func TestDepthCollector_QueueDepth(t *testing.T) {
	t.Parallel()
	q := &fakeQuerier{rows: []StateCount{
		{Queue: "transcode", State: "available", Tenant: "a", Count: 3},
		{Queue: "transcode", State: "available", Tenant: "b", Count: 2},
		{Queue: "transcode", State: "running", Count: 1},
		{Queue: "transcode", State: "completed", Count: 50},
		{Queue: "archive", State: "completed", Count: 9},
	}}
	c, _ := newTestCollector(t, q, MetricsOptions{Queues: []string{"idle"}})
	ctx := context.Background()
	cases := map[string]int64{"transcode": 6, "archive": 0, "idle": 0, DefaultQueue: 0}
	for queue, want := range cases {
		got, err := c.QueueDepth(ctx, queue)
		if err != nil || got != want {
			t.Errorf("QueueDepth(%q) = %d, %v; want %d", queue, got, err, want)
		}
	}
	if _, err := c.QueueDepth(ctx, "nope"); !errors.Is(err, ErrUnknownQueue) {
		t.Fatalf("QueueDepth(unknown) error = %v, want ErrUnknownQueue", err)
	}
}

func TestNewDepthCollector_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	var nilQuerier *fakeQuerier
	if _, err := NewDepthCollector(nilQuerier, MetricsOptions{}, nil, logger); err == nil {
		t.Error("accepted nil querier")
	}
	if _, err := NewDepthCollector(&fakeQuerier{}, MetricsOptions{}, nil, nil); err == nil {
		t.Error("accepted nil logger")
	}
	if _, err := NewDepthCollector(&fakeQuerier{}, MetricsOptions{CacheTTL: -1}, nil, logger); err == nil {
		t.Error("accepted negative cache TTL")
	}
	if _, err := NewPgxDepthQuerier(nil); err == nil {
		t.Error("accepted nil pool")
	}
}

func TestMetricsOptionsFor_AddsConfiguredQueues(t *testing.T) {
	t.Parallel()
	got := metricsOptionsFor(Options{
		Queue:   QueueOptions{Queues: map[string]QueueConfig{"critical": {Max: 1}}},
		Metrics: MetricsOptions{Queues: []string{"bulk"}},
	})
	if len(got.Queues) != 2 {
		t.Fatalf("queues = %v, want bulk + critical", got.Queues)
	}
}

func TestSecondsToAge_ClampsNegative(t *testing.T) {
	t.Parallel()
	if got := secondsToAge(-3); got != 0 {
		t.Fatalf("secondsToAge(-3) = %v, want 0", got)
	}
	if got := secondsToAge(1.5); got != 1500*time.Millisecond {
		t.Fatalf("secondsToAge(1.5) = %v, want 1.5s", got)
	}
}

func TestTracingPlugins(t *testing.T) {
	t.Parallel()
	if got := tracingPlugins(TracingOptions{}); len(got) != 0 {
		t.Fatalf("disabled tracing plugins = %d, want 0", len(got))
	}
	if got := tracingPlugins(TracingOptions{Enabled: true}); len(got) != 1 {
		t.Fatalf("enabled tracing plugins = %d, want 1", len(got))
	}
}
