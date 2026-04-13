package sentry

import (
	"context"
	"fmt"
	"log/slog"

	sentrygo "github.com/getsentry/sentry-go"
)

// sentryHandler is a slog.Handler that forwards Error-level records to
// Sentry as events + warn-level as breadcrumbs. Lower levels are ignored.
type sentryHandler struct {
	attrs []slog.Attr
	group string
}

func newSentryHandler() slog.Handler { return &sentryHandler{} }

func (h *sentryHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= slog.LevelWarn
}

func (h *sentryHandler) Handle(_ context.Context, r slog.Record) error {
	hub := sentrygo.CurrentHub().Clone()
	hub.WithScope(func(scope *sentrygo.Scope) {
		for _, a := range h.attrs {
			scope.SetTag(a.Key, fmt.Sprintf("%v", a.Value.Any()))
		}
		r.Attrs(func(a slog.Attr) bool {
			scope.SetTag(a.Key, fmt.Sprintf("%v", a.Value.Any()))
			return true
		})
		switch {
		case r.Level >= slog.LevelError:
			scope.SetLevel(sentrygo.LevelError)
			hub.CaptureMessage(r.Message)
		case r.Level >= slog.LevelWarn:
			hub.AddBreadcrumb(&sentrygo.Breadcrumb{
				Type:     "default",
				Category: "log",
				Message:  r.Message,
				Level:    sentrygo.LevelWarning,
			}, nil)
		}
	})
	return nil
}

func (h *sentryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sentryHandler{
		attrs: append(append([]slog.Attr{}, h.attrs...), attrs...),
		group: h.group,
	}
}

func (h *sentryHandler) WithGroup(name string) slog.Handler {
	return &sentryHandler{attrs: h.attrs, group: name}
}

// fanoutHandler writes each record to every wrapped handler. Mirrors the
// otel fanout — factoring this to an internal package is worth doing once
// both are stable.
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
