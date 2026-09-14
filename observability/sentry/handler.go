// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sentry

import (
	"context"
	"fmt"
	"log/slog"

	sentrygo "github.com/getsentry/sentry-go"

	corelog "github.com/golusoris/golusoris/core/log"
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

// fanoutHandler writes each record to every wrapped handler. Shared with the
// otel bridge via core/log.FanoutHandler.
type fanoutHandler = corelog.FanoutHandler
