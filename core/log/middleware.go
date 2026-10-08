// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package log

import (
	"cmp"
	"log/slog"
	"slices"

	"go.uber.org/fx"
)

// MiddlewareGroup is the fx value group [Module] reads [HandlerMiddleware]
// from. Other modules contribute with [AsMiddleware].
const MiddlewareGroup = "golusoris.log.middleware"

// HandlerMiddleware decorates the handler [New] builds, before pod attributes
// are attached. Modules use it to add cross-cutting behaviour (trace IDs, log
// export) to the injected *slog.Logger without core/log importing them.
type HandlerMiddleware struct {
	// Name identifies the middleware in diagnostics and orders equal Orders.
	Name string
	// Order sorts middleware: lower values wrap closer to the base handler.
	Order int
	// Wrap returns next decorated. A nil Wrap leaves the handler unchanged,
	// so a disabled feature can still satisfy its provider.
	Wrap func(next slog.Handler) slog.Handler
}

// AsMiddleware annotates ctor (any fx constructor returning a
// [HandlerMiddleware]) so its result joins [MiddlewareGroup].
func AsMiddleware(ctor any) any {
	return fx.Annotate(ctor, fx.ResultTags(`group:"`+MiddlewareGroup+`"`))
}

// applyMiddleware wraps h with every non-nil middleware in (Order, Name)
// order; the input slice is left untouched.
func applyMiddleware(h slog.Handler, mws []HandlerMiddleware) slog.Handler {
	if len(mws) == 0 {
		return h
	}
	sorted := slices.Clone(mws)
	slices.SortStableFunc(sorted, func(a, b HandlerMiddleware) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.Name, b.Name))
	})
	for _, mw := range sorted {
		if mw.Wrap == nil {
			continue
		}
		if wrapped := mw.Wrap(h); wrapped != nil {
			h = wrapped
		}
	}
	return h
}

// moduleParams collects the logger options plus every contributed middleware.
type moduleParams struct {
	fx.In

	Options    Options
	Middleware []HandlerMiddleware `group:"golusoris.log.middleware"`
}
