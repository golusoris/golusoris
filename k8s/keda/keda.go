// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package keda serves KEDA's external scaler gRPC protocol over jobs queue
// depth, so a ScaledObject can scale River workers on outstanding jobs,
// including to and from zero. The protocol stubs are generated from KEDA's
// externalscaler.proto (internal/externalscalerpb) instead of importing the
// kedacore/keda/v2 module and its operator dependency tree.
//
// ScaledObject trigger:
//
//	triggers:
//	  - type: external
//	    metadata:
//	      scalerAddress: my-controller.ns.svc:9090
//	      queue: transcode          # required
//	      targetDepth: "5"          # jobs per replica (default k8s.keda.target_depth)
//	      activationDepth: "0"      # active above this depth (default k8s.keda.activation_depth)
//
// Config keys (env: APP_K8S_KEDA_*):
//
//	k8s.keda.target_depth      # default jobs per replica (default 10)
//	k8s.keda.activation_depth  # default activation threshold (default 0)
//	k8s.keda.stream_interval   # StreamIsActive re-check period (default 10s)
//	k8s.keda.query_timeout     # bound per depth lookup (default 5s)
package keda

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/jonboulle/clockwork"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/k8s/keda/internal/externalscalerpb"
)

// ScaledObject trigger metadata keys.
const (
	MetaQueue           = "queue"
	MetaTargetDepth     = "targetDepth"
	MetaActivationDepth = "activationDepth"
)

const metricPrefix = "river-queue-"

// DepthSource reports outstanding jobs in a queue. *jobs.DepthCollector
// satisfies it (available + running); jobs.ErrUnknownQueue maps to NotFound.
type DepthSource interface {
	QueueDepth(ctx context.Context, queue string) (int64, error)
}

// Options are defaults for triggers that omit metadata. Config: k8s.keda.*.
type Options struct {
	// TargetDepth is the jobs-per-replica target the HPA divides by.
	TargetDepth float64 `koanf:"target_depth"`
	// ActivationDepth: the queue is active (scale from zero) above this depth.
	ActivationDepth int64 `koanf:"activation_depth"`
	// StreamInterval is how often StreamIsActive re-reads depth.
	StreamInterval time.Duration `koanf:"stream_interval"`
	// QueryTimeout bounds each depth lookup.
	QueryTimeout time.Duration `koanf:"query_timeout"`
}

// DefaultOptions returns target 10, activation 0, 10s stream interval, 5s query bound.
func DefaultOptions() Options {
	return Options{TargetDepth: 10, StreamInterval: 10 * time.Second, QueryTimeout: 5 * time.Second}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.TargetDepth == 0 {
		o.TargetDepth = d.TargetDepth
	}
	if o.StreamInterval == 0 {
		o.StreamInterval = d.StreamInterval
	}
	if o.QueryTimeout == 0 {
		o.QueryTimeout = d.QueryTimeout
	}
	return o
}

func (o Options) validate() error {
	if o.TargetDepth < 0 || o.ActivationDepth < 0 || o.StreamInterval < 0 || o.QueryTimeout < 0 {
		return errors.New("k8s/keda: options must not be negative")
	}
	return nil
}

// Scaler implements KEDA's externalscaler.ExternalScaler gRPC service.
type Scaler struct {
	src    DepthSource
	opts   Options
	clock  clock.Clock
	logger *slog.Logger
}

// NewScaler builds a scaler over src. clk drives StreamIsActive (nil = wall clock).
func NewScaler(src DepthSource, opts Options, clk clock.Clock, logger *slog.Logger) (*Scaler, error) {
	if validate.IsNil(src) {
		return nil, errors.New("k8s/keda: nil depth source")
	}
	if logger == nil {
		return nil, errors.New("k8s/keda: nil logger")
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return &Scaler{src: src, opts: opts.withDefaults(), clock: clk, logger: logger}, nil
}

// Register adds s to srv under KEDA's service name externalscaler.ExternalScaler.
func Register(srv grpc.ServiceRegistrar, s *Scaler) error {
	if validate.IsNil(srv) || s == nil {
		return errors.New("k8s/keda: register: nil server or scaler")
	}
	externalscalerpb.RegisterExternalScalerServer(srv, s)
	return nil
}

// trigger is one ScaledObject's parsed metadata.
type trigger struct {
	queue      string
	target     float64
	activation int64
}

func (t trigger) metricName() string { return metricPrefix + t.queue }

func (s *Scaler) parse(ref *externalscalerpb.ScaledObjectRef) (trigger, error) {
	md := ref.GetScalerMetadata()
	t := trigger{queue: md[MetaQueue], target: s.opts.TargetDepth, activation: s.opts.ActivationDepth}
	if t.queue == "" {
		return trigger{}, status.Errorf(codes.InvalidArgument, "k8s/keda: metadata %q is required", MetaQueue)
	}
	if raw, ok := md[MetaTargetDepth]; ok {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
			return trigger{}, status.Errorf(codes.InvalidArgument, "k8s/keda: %s %q must be a positive number", MetaTargetDepth, raw)
		}
		t.target = v
	}
	if raw, ok := md[MetaActivationDepth]; ok {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return trigger{}, status.Errorf(codes.InvalidArgument, "k8s/keda: %s %q must be a non-negative integer", MetaActivationDepth, raw)
		}
		t.activation = v
	}
	return t, nil
}

func (s *Scaler) depth(ctx context.Context, t trigger) (int64, error) {
	qctx, cancel := context.WithTimeout(ctx, s.opts.QueryTimeout)
	defer cancel()
	d, err := s.src.QueueDepth(qctx, t.queue)
	switch {
	case err == nil:
		return d, nil
	case errors.Is(err, jobs.ErrUnknownQueue):
		return 0, status.Errorf(codes.NotFound, "k8s/keda: queue %q: %v", t.queue, err)
	default:
		s.logger.WarnContext(ctx, "k8s/keda: depth lookup failed", slog.String("queue", t.queue), slog.String("error", err.Error()))
		return 0, status.Errorf(codes.Unavailable, "k8s/keda: queue %q depth: %v", t.queue, err)
	}
}

func (s *Scaler) active(ctx context.Context, ref *externalscalerpb.ScaledObjectRef) (bool, error) {
	t, err := s.parse(ref)
	if err != nil {
		return false, err
	}
	d, err := s.depth(ctx, t)
	if err != nil {
		return false, err
	}
	return d > t.activation, nil
}

// IsActive reports whether the queue holds more than activationDepth jobs;
// false lets KEDA scale the workload to zero.
func (s *Scaler) IsActive(ctx context.Context, ref *externalscalerpb.ScaledObjectRef) (*externalscalerpb.IsActiveResponse, error) {
	ok, err := s.active(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &externalscalerpb.IsActiveResponse{Result: ok}, nil
}

// StreamIsActive pushes the activity state on open and whenever it changes,
// re-reading depth every StreamInterval until KEDA closes the stream.
func (s *Scaler) StreamIsActive(ref *externalscalerpb.ScaledObjectRef, stream grpc.ServerStreamingServer[externalscalerpb.IsActiveResponse]) error {
	ctx := stream.Context()
	ticker := s.clock.NewTicker(s.opts.StreamInterval)
	defer ticker.Stop()
	sent, last := false, false
	for ctx.Err() == nil {
		ok, err := s.active(ctx, ref)
		if err != nil {
			return err
		}
		if !sent || ok != last {
			if err := stream.Send(&externalscalerpb.IsActiveResponse{Result: ok}); err != nil {
				return fmt.Errorf("k8s/keda: stream send: %w", err)
			}
			sent, last = true, ok
		}
		select {
		case <-ctx.Done():
		case <-ticker.Chan():
		}
	}
	return nil
}

// GetMetricSpec returns the per-replica target for the queue's metric.
func (s *Scaler) GetMetricSpec(_ context.Context, ref *externalscalerpb.ScaledObjectRef) (*externalscalerpb.GetMetricSpecResponse, error) {
	t, err := s.parse(ref)
	if err != nil {
		return nil, err
	}
	return &externalscalerpb.GetMetricSpecResponse{MetricSpecs: []*externalscalerpb.MetricSpec{{
		MetricName:      t.metricName(),
		TargetSize:      int64(math.Ceil(t.target)),
		TargetSizeFloat: t.target,
	}}}, nil
}

// GetMetrics returns the queue's outstanding job count.
func (s *Scaler) GetMetrics(ctx context.Context, req *externalscalerpb.GetMetricsRequest) (*externalscalerpb.GetMetricsResponse, error) {
	t, err := s.parse(req.GetScaledObjectRef())
	if err != nil {
		return nil, err
	}
	d, err := s.depth(ctx, t)
	if err != nil {
		return nil, err
	}
	return &externalscalerpb.GetMetricsResponse{MetricValues: []*externalscalerpb.MetricValue{{
		MetricName:       t.metricName(),
		MetricValue:      d,
		MetricValueFloat: float64(d),
	}}}, nil
}

// StreamMetricSpec is optional in the protocol; Unimplemented makes KEDA fall
// back to polling GetMetricSpec, which suits static per-trigger targets.
func (*Scaler) StreamMetricSpec(*externalscalerpb.ScaledObjectRef, grpc.ServerStreamingServer[externalscalerpb.GetMetricSpecResponse]) error {
	return status.Error(codes.Unimplemented, "k8s/keda: metric specs are static; poll GetMetricSpec") //nolint:wrapcheck // gRPC status must reach the transport as-is
}

// Module provides *Scaler over *jobs.DepthCollector (jobs.MetricsModule) and
// registers it on the *grpc.Server from golusoris grpc.Module.
var Module = fx.Module(
	"golusoris.k8s.keda",
	fx.Provide(loadOptions),
	fx.Provide(provideScaler),
	fx.Invoke(func(srv *grpc.Server, s *Scaler) error { return Register(srv, s) }),
)

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("k8s.keda", &opts); err != nil {
		return Options{}, fmt.Errorf("k8s/keda: load options: %w", err)
	}
	return opts, nil
}

type scalerParams struct {
	fx.In
	Collector *jobs.DepthCollector
	Opts      Options
	Logger    *slog.Logger
	Clock     clock.Clock `optional:"true"`
}

func provideScaler(p scalerParams) (*Scaler, error) {
	return NewScaler(p.Collector, p.Opts, p.Clock, p.Logger)
}
