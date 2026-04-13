package otel

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/attribute"
	otlplog "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlpmetric "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlptrace "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

// attributeKV is a small local type — semconv keys are typed attribute.Key
// values, so we carry pairs through toKVs without resolving at literal sites.
type attributeKV struct {
	Key attribute.Key
	Val string
}

func toKVs(in []attributeKV) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(in))
	for _, kv := range in {
		if kv.Val == "" {
			continue
		}
		out = append(out, kv.Key.String(kv.Val))
	}
	return out
}

// podInfoAttrs pulls the k8s downward-API env vars (same set used by log/)
// and maps them to OTel resource attributes. Empty env vars are skipped.
func podInfoAttrs() []attributeKV {
	vars := []struct {
		env string
		key attribute.Key
	}{
		{"POD_NAME", "k8s.pod.name"},
		{"POD_NAMESPACE", "k8s.namespace.name"},
		{"POD_IP", "k8s.pod.ip"},
		{"NODE_NAME", "k8s.node.name"},
		{"SERVICE_ACCOUNT", "k8s.service_account.name"},
	}
	out := make([]attributeKV, 0, len(vars))
	for _, v := range vars {
		if val := os.Getenv(v.env); val != "" {
			out = append(out, attributeKV{v.key, val})
		}
	}
	return out
}

func dialOpts(opts Options) []otlptrace.Option {
	o := []otlptrace.Option{}
	if opts.Endpoint != "" {
		o = append(o, otlptrace.WithEndpoint(opts.Endpoint))
	}
	if opts.Insecure {
		o = append(o, otlptrace.WithInsecure())
	}
	return o
}

func metricDialOpts(opts Options) []otlpmetric.Option {
	o := []otlpmetric.Option{}
	if opts.Endpoint != "" {
		o = append(o, otlpmetric.WithEndpoint(opts.Endpoint))
	}
	if opts.Insecure {
		o = append(o, otlpmetric.WithInsecure())
	}
	return o
}

func logDialOpts(opts Options) []otlplog.Option {
	o := []otlplog.Option{}
	if opts.Endpoint != "" {
		o = append(o, otlplog.WithEndpoint(opts.Endpoint))
	}
	if opts.Insecure {
		o = append(o, otlplog.WithInsecure())
	}
	return o
}

// fanoutHandler writes each record to every wrapped handler. Used by
// ModuleWithSlogBridge so slog logs appear in both the local handler
// (stdout/tint/JSON) and the OTel exporter.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (f *fanoutHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			return err //nolint:wrapcheck // fan-out: error context already descriptive
		}
	}
	return nil
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		cloned[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: cloned}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	cloned := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		cloned[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: cloned}
}
