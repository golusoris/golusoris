// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package keda

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	dbsqlite "github.com/golusoris/golusoris/db/sqlite"
	"github.com/golusoris/golusoris/jobs"
	jobsqlite "github.com/golusoris/golusoris/jobs/sqlite"
	"github.com/golusoris/golusoris/k8s/keda/internal/externalscalerpb"
)

// fakeSource returns depth per queue; unknown queues return jobs.ErrUnknownQueue.
type fakeSource struct {
	depth map[string]*atomic.Int64
	err   error
}

func newFakeSource(depths map[string]int64) *fakeSource {
	f := &fakeSource{depth: map[string]*atomic.Int64{}}
	for q, d := range depths {
		v := &atomic.Int64{}
		v.Store(d)
		f.depth[q] = v
	}
	return f
}

func (f *fakeSource) QueueDepth(_ context.Context, queue string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	v, ok := f.depth[queue]
	if !ok {
		return 0, fmt.Errorf("fake: %w", jobs.ErrUnknownQueue)
	}
	return v.Load(), nil
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// dial serves s over bufconn and returns a KEDA-side client.
func dial(t *testing.T, s *Scaler) externalscalerpb.ExternalScalerClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	if err := Register(srv, s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return externalscalerpb.NewExternalScalerClient(conn)
}

func newScaler(t *testing.T, src DepthSource, clk clock.Clock) *Scaler {
	t.Helper()
	s, err := NewScaler(src, Options{}, clk, discard())
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	return s
}

func ref(md map[string]string) *externalscalerpb.ScaledObjectRef {
	return &externalscalerpb.ScaledObjectRef{Name: "workers", Namespace: "vmafx", ScalerMetadata: md}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestScaler_ActiveQueueReportsMetric(t *testing.T) {
	t.Parallel()
	client := dial(t, newScaler(t, newFakeSource(map[string]int64{"transcode": 7}), nil))
	ctx := testCtx(t)
	r := ref(map[string]string{MetaQueue: "transcode", MetaTargetDepth: "2.5"})

	active, err := client.IsActive(ctx, r)
	if err != nil || !active.GetResult() {
		t.Fatalf("IsActive = %v, %v; want true", active, err)
	}
	spec, err := client.GetMetricSpec(ctx, r)
	if err != nil {
		t.Fatalf("GetMetricSpec: %v", err)
	}
	ms := spec.GetMetricSpecs()[0]
	if ms.GetMetricName() != "river-queue-transcode" || ms.GetTargetSizeFloat() != 2.5 || ms.GetTargetSize() != 3 {
		t.Fatalf("metric spec = %+v, want river-queue-transcode target 2.5", ms)
	}
	metrics, err := client.GetMetrics(ctx, &externalscalerpb.GetMetricsRequest{ScaledObjectRef: r, MetricName: ms.GetMetricName()})
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	mv := metrics.GetMetricValues()[0]
	if mv.GetMetricName() != ms.GetMetricName() || mv.GetMetricValue() != 7 || mv.GetMetricValueFloat() != 7 {
		t.Fatalf("metric value = %+v, want 7", mv)
	}
}

func TestScaler_EmptyQueueIsInactiveForScaleToZero(t *testing.T) {
	t.Parallel()
	client := dial(t, newScaler(t, newFakeSource(map[string]int64{"transcode": 0, "bulk": 3}), nil))
	ctx := testCtx(t)
	cases := []struct {
		md   map[string]string
		want bool
	}{
		{map[string]string{MetaQueue: "transcode"}, false},
		{map[string]string{MetaQueue: "bulk", MetaActivationDepth: "3"}, false},
		{map[string]string{MetaQueue: "bulk", MetaActivationDepth: "2"}, true},
	}
	for _, tc := range cases {
		got, err := client.IsActive(ctx, ref(tc.md))
		if err != nil || got.GetResult() != tc.want {
			t.Errorf("IsActive(%v) = %v, %v; want %v", tc.md, got.GetResult(), err, tc.want)
		}
	}
	spec, err := client.GetMetricSpec(ctx, ref(map[string]string{MetaQueue: "transcode"}))
	if err != nil || spec.GetMetricSpecs()[0].GetTargetSizeFloat() != DefaultOptions().TargetDepth {
		t.Fatalf("GetMetricSpec default target = %v, %v", spec, err)
	}
}

func TestScaler_ErrorCodes(t *testing.T) {
	t.Parallel()
	client := dial(t, newScaler(t, newFakeSource(map[string]int64{"transcode": 1}), nil))
	ctx := testCtx(t)
	cases := map[string]struct {
		md   map[string]string
		want codes.Code
	}{
		"unknown queue":         {map[string]string{MetaQueue: "nope"}, codes.NotFound},
		"missing queue":         {map[string]string{}, codes.InvalidArgument},
		"zero target":           {map[string]string{MetaQueue: "transcode", MetaTargetDepth: "0"}, codes.InvalidArgument},
		"non-numeric target":    {map[string]string{MetaQueue: "transcode", MetaTargetDepth: "lots"}, codes.InvalidArgument},
		"negative activation":   {map[string]string{MetaQueue: "transcode", MetaActivationDepth: "-1"}, codes.InvalidArgument},
		"fractional activation": {map[string]string{MetaQueue: "transcode", MetaActivationDepth: "1.5"}, codes.InvalidArgument},
	}
	for name, tc := range cases {
		_, err := client.GetMetrics(ctx, &externalscalerpb.GetMetricsRequest{ScaledObjectRef: ref(tc.md)})
		if got := status.Code(err); got != tc.want {
			t.Errorf("%s: GetMetrics code = %v (%v), want %v", name, got, err, tc.want)
		}
	}
	if _, err := client.IsActive(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("IsActive(nil ref) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestScaler_SourceFailureIsUnavailable(t *testing.T) {
	t.Parallel()
	src := newFakeSource(nil)
	src.err = errors.New("db down")
	client := dial(t, newScaler(t, src, nil))
	_, err := client.IsActive(testCtx(t), ref(map[string]string{MetaQueue: "transcode"}))
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("IsActive code = %v (%v), want Unavailable", status.Code(err), err)
	}
}

func TestScaler_StreamIsActivePushesChanges(t *testing.T) {
	t.Parallel()
	src := newFakeSource(map[string]int64{"transcode": 0})
	clk := clockwork.NewFakeClock()
	client := dial(t, newScaler(t, src, clk))
	ctx := testCtx(t)
	stream, err := client.StreamIsActive(ctx, ref(map[string]string{MetaQueue: "transcode"}))
	if err != nil {
		t.Fatalf("StreamIsActive: %v", err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetResult() {
		t.Fatalf("first stream value = %v, %v; want inactive", first, err)
	}
	src.depth["transcode"].Store(4)
	if berr := clk.BlockUntilContext(ctx, 1); berr != nil {
		t.Fatalf("ticker never armed: %v", berr)
	}
	clk.Advance(DefaultOptions().StreamInterval)
	next, err := stream.Recv()
	if err != nil || !next.GetResult() {
		t.Fatalf("second stream value = %v, %v; want active", next, err)
	}
}

func TestScaler_StreamMetricSpecUnimplemented(t *testing.T) {
	t.Parallel()
	client := dial(t, newScaler(t, newFakeSource(nil), nil))
	stream, err := client.StreamMetricSpec(testCtx(t), ref(map[string]string{MetaQueue: "transcode"}))
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("StreamMetricSpec code = %v, want Unimplemented", status.Code(err))
	}
}

func TestNewScaler_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	var nilSource *fakeSource
	if _, err := NewScaler(nilSource, Options{}, nil, discard()); err == nil {
		t.Error("accepted nil source")
	}
	if _, err := NewScaler(newFakeSource(nil), Options{}, nil, nil); err == nil {
		t.Error("accepted nil logger")
	}
	if _, err := NewScaler(newFakeSource(nil), Options{TargetDepth: -1}, nil, discard()); err == nil {
		t.Error("accepted negative target")
	}
	if err := Register(nil, nil); err == nil {
		t.Error("Register accepted nil server")
	}
}

// TestModule_endToEndOverSQLite runs the real chain without Docker: SQLite
// queue -> jobs.MetricsModule collector -> keda.Module -> gRPC client.
func TestModule_endToEndOverSQLite(t *testing.T) {
	t.Parallel()
	db, err := dbsqlite.Open(context.Background(), dbsqlite.Options{Path: filepath.Join(t.TempDir(), "q.db"), MaxOpenConns: 1}, discard())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg, err := config.New(config.Options{EnvPrefix: "KEDA_TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	clk := clockwork.NewFakeClock()
	var (
		producer *jobs.ClientSQL
		scaler   *Scaler
	)
	app := fxtest.New(t,
		fx.Supply(db, cfg, discard(), srv),
		fx.Provide(func() clock.Clock { return clk }),
		fx.Replace(jobs.Options{Enabled: true, ProducerOnly: true, Metrics: jobs.MetricsOptions{Queues: []string{"transcode"}}}),
		jobsqlite.Module,
		jobs.MetricsModule,
		Module,
		fx.Populate(&producer, &scaler),
	)
	app.RequireStart()
	defer app.RequireStop()
	if _, ok := srv.GetServiceInfo()["externalscaler.ExternalScaler"]; !ok {
		t.Fatal("keda.Module did not register externalscaler.ExternalScaler on the grpc server")
	}

	client := dial(t, scaler)
	ctx := testCtx(t)
	r := ref(map[string]string{MetaQueue: "transcode"})
	if active, aerr := client.IsActive(ctx, r); aerr != nil || active.GetResult() {
		t.Fatalf("empty configured queue IsActive = %v, %v; want inactive", active, aerr)
	}
	for i := range 3 {
		if _, ierr := producer.Insert(ctx, probeArgs{N: i}, &jobs.InsertOpts{Queue: "transcode"}); ierr != nil {
			t.Fatalf("Insert: %v", ierr)
		}
	}
	clk.Advance(jobs.DefaultMetricsOptions().CacheTTL) // expire the cached empty snapshot
	metrics, err := client.GetMetrics(ctx, &externalscalerpb.GetMetricsRequest{ScaledObjectRef: r})
	if err != nil || metrics.GetMetricValues()[0].GetMetricValue() != 3 {
		t.Fatalf("GetMetrics = %v, %v; want 3", metrics, err)
	}
}

type probeArgs struct {
	N int `json:"n"`
}

func (probeArgs) Kind() string { return "keda-probe" }
