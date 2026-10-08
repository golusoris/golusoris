// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package otel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	otelapi "go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/log"
	golusoris_otel "github.com/golusoris/golusoris/otel"
)

var (
	testTraceID = trace.TraceID{0x0a, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	testSpanID  = trace.SpanID{0x0b, 1, 2, 3, 4, 5, 6, 7}
)

func spanContext(t *testing.T, flags trace.TraceFlags) context.Context {
	t.Helper()
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: testTraceID, SpanID: testSpanID, TraceFlags: flags})
	return trace.ContextWithSpanContext(t.Context(), sc)
}

func decodeRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output not JSON: %v (%q)", err, buf.String())
	}
	return rec
}

func TestTraceHandlerAddsSpanContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(golusoris_otel.TraceHandler(slog.NewJSONHandler(&buf, nil)))
	logger.InfoContext(spanContext(t, trace.FlagsSampled), "hello")

	rec := decodeRecord(t, &buf)
	want := map[string]string{
		golusoris_otel.TraceIDKey:    testTraceID.String(),
		golusoris_otel.SpanIDKey:     testSpanID.String(),
		golusoris_otel.TraceFlagsKey: "01",
	}
	for key, value := range want {
		if rec[key] != value {
			t.Errorf("%s = %v, want %s", key, rec[key], value)
		}
	}
}

func TestTraceHandlerPassesRecordsWithoutSpan(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(golusoris_otel.TraceHandler(slog.NewJSONHandler(&buf, nil)))
	logger.InfoContext(t.Context(), "hello")

	rec := decodeRecord(t, &buf)
	if _, ok := rec[golusoris_otel.TraceIDKey]; ok {
		t.Fatalf("trace_id stamped without a span: %v", rec)
	}
}

func TestTraceHandlerKeepsDerivedHandlersAndLevels(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(golusoris_otel.TraceHandler(base)).With("k", "v").WithGroup("g")
	ctx := spanContext(t, 0)

	logger.InfoContext(ctx, "filtered")
	if buf.Len() != 0 {
		t.Fatalf("Enabled must delegate the level filter, got %q", buf.String())
	}
	logger.WarnContext(ctx, "kept")
	rec := decodeRecord(t, &buf)
	if rec["k"] != "v" {
		t.Errorf("WithAttrs lost: %v", rec)
	}
	group, ok := rec["g"].(map[string]any)
	if !ok || group[golusoris_otel.TraceFlagsKey] != "00" {
		t.Errorf("unsampled flags under open group = %v, want 00", rec["g"])
	}
}

// moduleLogger boots config + log + otel modules with JSON output captured in buf.
func moduleLogger(t *testing.T, buf *bytes.Buffer, extra ...fx.Option) *slog.Logger {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("APP_OTEL_SERVICE_NAME", "test")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_SDK_DISABLED", "")

	var logger *slog.Logger
	opts := append([]fx.Option{
		fx.Provide(func() (*config.Config, error) {
			return config.New(config.Options{EnvPrefix: "APP_", Delimiter: ".", CompoundKeys: []string{"otel.logs.trace_ids"}})
		}),
		log.Module,
		fx.Decorate(func(o log.Options) log.Options {
			o.Format, o.Output = log.FormatJSON, buf
			return o
		}),
		golusoris_otel.Module,
		fx.Populate(&logger),
	}, extra...)
	app := fxtest.New(t, opts...)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	buf.Reset()
	return logger
}

func TestModuleStampsTraceIDsOnInjectedLogger(t *testing.T) { //nolint:paralleltest // t.Setenv + slog.Default.
	var buf bytes.Buffer
	logger := moduleLogger(t, &buf)
	logger.InfoContext(spanContext(t, trace.FlagsSampled), "hello")
	if rec := decodeRecord(t, &buf); rec[golusoris_otel.TraceIDKey] != testTraceID.String() {
		t.Fatalf("injected logger lacks trace_id: %v", rec)
	}
}

func TestModuleTraceIDsDisabledByConfig(t *testing.T) {
	t.Setenv("APP_OTEL_LOGS_TRACE_IDS", "false")
	var buf bytes.Buffer
	logger := moduleLogger(t, &buf)
	logger.InfoContext(spanContext(t, trace.FlagsSampled), "hello")
	if rec := decodeRecord(t, &buf); rec[golusoris_otel.TraceIDKey] != nil {
		t.Fatalf("otel.logs.trace_ids=false still stamped: %v", rec)
	}
}

func TestModuleTraceIDsOffWhenOTelDisabled(t *testing.T) {
	t.Setenv("APP_OTEL_ENABLED", "false")
	var buf bytes.Buffer
	logger := moduleLogger(t, &buf)
	logger.InfoContext(spanContext(t, trace.FlagsSampled), "hello")
	if rec := decodeRecord(t, &buf); rec[golusoris_otel.TraceIDKey] != nil {
		t.Fatalf("otel.enabled=false still stamped: %v", rec)
	}
}

// memoryExporter keeps exported OTel log records for assertions.
type memoryExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *memoryExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range records {
		e.records = append(e.records, r.Clone())
	}
	return nil
}

func (e *memoryExporter) Shutdown(context.Context) error   { return nil }
func (e *memoryExporter) ForceFlush(context.Context) error { return nil }

func (e *memoryExporter) bodies() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.records))
	for _, r := range e.records {
		out = append(out, r.Body().AsString())
	}
	return out
}

func TestSlogBridgeExportsInjectedLoggerOnce(t *testing.T) { //nolint:paralleltest // t.Setenv + slog.Default.
	exporter := &memoryExporter{}
	providers := &golusoris_otel.Providers{
		Logger: sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter))),
	}
	var buf bytes.Buffer
	logger := moduleLogger(t, &buf,
		fx.Decorate(func(*golusoris_otel.Providers) *golusoris_otel.Providers { return providers }),
		golusoris_otel.ModuleWithSlogBridge,
	)
	logger.InfoContext(spanContext(t, trace.FlagsSampled), "bridged")

	bridged := 0
	for _, body := range exporter.bodies() {
		if body == "bridged" {
			bridged++
		}
	}
	if bridged != 1 {
		t.Fatalf("bridged record exported %d times, want once", bridged)
	}
	if slog.Default() != logger {
		t.Fatal("slog.Default must stay the injected logger when core/log applied the bridge")
	}
	if rec := decodeRecord(t, &buf); rec[golusoris_otel.TraceIDKey] != testTraceID.String() {
		t.Fatalf("stdout record lacks trace_id: %v", rec)
	}
}

func TestModuleBuildsProvidersWithoutConsumer(t *testing.T) {
	prevTP := otelapi.GetTracerProvider()
	t.Cleanup(func() { otelapi.SetTracerProvider(prevTP) })
	t.Setenv("APP_OTEL_ENDPOINT", "127.0.0.1:1")
	t.Setenv("APP_OTEL_EXPORT_METRICS", "false")
	t.Setenv("APP_OTEL_EXPORT_LOGS", "false")

	var buf bytes.Buffer
	moduleLogger(t, &buf)
	if _, ok := otelapi.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatalf("global tracer provider = %T; otel.Module alone must install the SDK provider at start", otelapi.GetTracerProvider())
	}
}
