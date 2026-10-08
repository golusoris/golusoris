// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package otel

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/trace"

	corelog "github.com/golusoris/golusoris/core/log"
)

// Record attribute keys [TraceHandler] writes. Loki derived fields and
// Grafana trace-to-logs links match on these names.
const (
	TraceIDKey    = "trace_id"
	SpanIDKey     = "span_id"
	TraceFlagsKey = "trace_flags"
)

// Middleware order: trace IDs wrap the base handler, the OTLP bridge wraps
// that, so stdout carries trace_id while OTLP records keep native context.
const (
	traceMiddlewareOrder  = 0
	bridgeMiddlewareOrder = 100
)

// LogsOptions tunes the slog integration.
type LogsOptions struct {
	// TraceIDs stamps trace_id, span_id and trace_flags from the record's
	// context span onto every slog record. Effective only when otel.enabled.
	TraceIDs bool `koanf:"trace_ids"`
}

// TraceHandler returns a handler that adds [TraceIDKey], [SpanIDKey] and
// [TraceFlagsKey] from the span in the record's context before delegating to
// next. Records without a valid span context pass through unchanged. Keys
// nest under any group opened with WithGroup, like every record attribute.
func TraceHandler(next slog.Handler) slog.Handler {
	return &traceHandler{next: next}
}

type traceHandler struct {
	next slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		// Clone: AddAttrs on a shared record copy would corrupt sibling handlers.
		r = r.Clone()
		r.AddAttrs(
			slog.String(TraceIDKey, sc.TraceID().String()),
			slog.String(SpanIDKey, sc.SpanID().String()),
			slog.String(TraceFlagsKey, sc.TraceFlags().String()),
		)
	}
	if err := h.next.Handle(ctx, r); err != nil {
		return fmt.Errorf("otel: trace handler: %w", err)
	}
	return nil
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}

// traceMiddleware contributes [TraceHandler] to core/log when otel is enabled
// and otel.logs.trace_ids is on; otherwise it contributes a no-op.
func traceMiddleware(opts Options) corelog.HandlerMiddleware {
	mw := corelog.HandlerMiddleware{Name: "otel.trace_ids", Order: traceMiddlewareOrder}
	if opts.Enabled && opts.Logs.TraceIDs {
		mw.Wrap = TraceHandler
	}
	return mw
}

// bridgeState records whether core/log applied the bridge middleware, so
// ModuleWithSlogBridge falls back to slog.Default only for app-supplied loggers.
type bridgeState struct {
	applied atomic.Bool
}

func newBridgeState() *bridgeState { return &bridgeState{} }

func newBridgeHandler(providers *Providers, opts Options) slog.Handler {
	return otelslog.NewHandler(opts.Service.Name, otelslog.WithLoggerProvider(providers.Logger))
}

// bridgeMiddleware fans every record of the injected logger out to the OTel
// logger provider; a no-op when log export is off.
func bridgeMiddleware(providers *Providers, opts Options, state *bridgeState) corelog.HandlerMiddleware {
	mw := corelog.HandlerMiddleware{Name: "otel.slog_bridge", Order: bridgeMiddlewareOrder}
	if providers == nil || providers.Logger == nil {
		return mw
	}
	mw.Wrap = func(next slog.Handler) slog.Handler {
		state.applied.Store(true)
		return &fanoutHandler{Handlers: []slog.Handler{next, newBridgeHandler(providers, opts)}}
	}
	return mw
}

// installDefaultBridge covers apps that supply their own *slog.Logger instead
// of core/log.Module: only slog.Default can carry the bridge there.
func installDefaultBridge(state *bridgeState, providers *Providers, existing *slog.Logger, opts Options) {
	if state.applied.Load() || existing == nil || providers == nil || providers.Logger == nil {
		return
	}
	slog.SetDefault(slog.New(&fanoutHandler{
		Handlers: []slog.Handler{existing.Handler(), newBridgeHandler(providers, opts)},
	}))
}
