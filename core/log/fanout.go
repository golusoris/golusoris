// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package log

import (
	"context"
	"log/slog"
)

// FanoutHandler writes each record to every wrapped handler. Shared by the
// otel and sentry bridge modules, which each fan slog out to the existing
// handler plus their own bridge handler.
type FanoutHandler struct {
	Handlers []slog.Handler
}

// Enabled reports whether any wrapped handler is enabled for lvl.
func (f *FanoutHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range f.Handlers {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}

// Handle forwards r to every wrapped handler that is enabled for its level.
func (f *FanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f.Handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			return err //nolint:wrapcheck // fan-out: error context already descriptive
		}
	}
	return nil
}

// WithAttrs returns a FanoutHandler wrapping each handler's own WithAttrs.
func (f *FanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := make([]slog.Handler, len(f.Handlers))
	for i, h := range f.Handlers {
		cloned[i] = h.WithAttrs(attrs)
	}
	return &FanoutHandler{Handlers: cloned}
}

// WithGroup returns a FanoutHandler wrapping each handler's own WithGroup.
func (f *FanoutHandler) WithGroup(name string) slog.Handler {
	cloned := make([]slog.Handler, len(f.Handlers))
	for i, h := range f.Handlers {
		cloned[i] = h.WithGroup(name)
	}
	return &FanoutHandler{Handlers: cloned}
}
