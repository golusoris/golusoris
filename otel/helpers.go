package otel

import (
	"context"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	otlplog "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlpmetric "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlptrace "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

// defaultServiceName derives a service.name when the operator did not set
// otel.service.name, so a shared bootstrap can enable OTel fleet-wide with no
// per-binary config (issue #254). Preference order: the module path's last
// segment from the embedded build info, else the argv[0] basename. Returns ""
// when neither yields a usable name — the caller then errors only if enabled.
func defaultServiceName() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if name := cleanServiceName(path.Base(bi.Path)); name != "" {
			return name
		}
	}
	if len(os.Args) > 0 {
		if name := cleanServiceName(filepath.Base(os.Args[0])); name != "" {
			return name
		}
	}
	return ""
}

// cleanServiceName strips a trailing platform extension and rejects the
// path.Base/filepath.Base sentinels ("." / "/" / "\\" / "") that signal an
// empty or root input, so they never leak through as a service name.
func cleanServiceName(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	switch name {
	case "", ".", string(filepath.Separator):
		return ""
	}
	return name
}

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

// otlpEndpointEnvVars are the standard OTel env vars the OTLP gRPC exporters
// honour at construction time. We mirror them so the module's "is an endpoint
// configured?" decision matches what the exporter would actually dial.
var otlpEndpointEnvVars = []string{
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
}

// exporterConfigured reports whether any OTLP endpoint is set, either via the
// golusoris config (otel.endpoint) or the standard OTEL_EXPORTER_OTLP_*_ENDPOINT
// env vars. When false, the module degrades to a silent no-op (12-factor
// default: no collector configured → no exporter, no network).
func exporterConfigured(opts Options) bool {
	if opts.Endpoint != "" {
		return true
	}
	for _, env := range otlpEndpointEnvVars {
		if os.Getenv(env) != "" {
			return true
		}
	}
	return false
}

// sdkDisabled honours the OTel-standard OTEL_SDK_DISABLED kill switch. The Go
// SDK does not act on it itself, so the module enforces it (matches the
// vmafx 12-factor convention referenced in issue #96).
func sdkDisabled() bool {
	return os.Getenv("OTEL_SDK_DISABLED") == "true"
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
