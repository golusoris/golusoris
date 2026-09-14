// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"fmt"
	"log/slog"

	xslog "golang.org/x/exp/slog"
)

// tusd v2.10.0 logs through golang.org/x/exp/slog, a distinct package from
// stdlib log/slog. xslogBridge adapts our injected *slog.Logger so tusd's
// internal logs flow through the framework's handler instead of a second sink.

// xslogLogger wraps a stdlib *slog.Logger as an *xslog.Logger for tusd.Config.
func xslogLogger(l *slog.Logger) *xslog.Logger {
	return xslog.New(&xslogBridge{h: l.Handler()})
}

// xslogBridge implements xslog.Handler by forwarding to a stdlib slog.Handler.
type xslogBridge struct {
	h slog.Handler
}

func (b *xslogBridge) Enabled(ctx context.Context, lvl xslog.Level) bool {
	return b.h.Enabled(ctx, slog.Level(lvl))
}

func (b *xslogBridge) Handle(ctx context.Context, r xslog.Record) error {
	out := slog.NewRecord(r.Time, slog.Level(r.Level), r.Message, r.PC)
	r.Attrs(func(a xslog.Attr) bool {
		out.AddAttrs(convAttr(a))
		return true
	})
	if err := b.h.Handle(ctx, out); err != nil {
		return fmt.Errorf("tus: forward log record: %w", err)
	}
	return nil
}

func (b *xslogBridge) WithAttrs(attrs []xslog.Attr) xslog.Handler {
	conv := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		conv = append(conv, convAttr(a))
	}
	return &xslogBridge{h: b.h.WithAttrs(conv)}
}

func (b *xslogBridge) WithGroup(name string) xslog.Handler {
	return &xslogBridge{h: b.h.WithGroup(name)}
}

const (
	// convAttrMaxDepth caps nested group depth; deeper groups are kept as an
	// opaque value instead of being descended into (HISS-02).
	convAttrMaxDepth = 64
	// convAttrMaxSteps caps the total attributes processed for one top-level
	// attr (HISS-02); a group larger than this is returned partially converted.
	convAttrMaxSteps = 1 << 16
)

// convFrame is one open group while convAttr flattens a nested tree.
type convFrame struct {
	key  string
	rest []xslog.Attr // children still to convert
	done []any        // converted children, in order
}

// convAttr converts an x/exp/slog Attr to a stdlib one, descending into groups
// with an explicit frame stack instead of recursion (HISS-01).
func convAttr(a xslog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() != xslog.KindGroup {
		return slog.Attr{Key: a.Key, Value: slog.AnyValue(v.Any())}
	}
	stack := []convFrame{{key: a.Key, rest: v.Group(), done: make([]any, 0, len(v.Group()))}}
	for range convAttrMaxSteps {
		top := &stack[len(stack)-1]
		if len(top.rest) == 0 { // group complete: fold it into its parent
			g := slog.Group(top.key, top.done...)
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return g
			}
			stack[len(stack)-1].done = append(stack[len(stack)-1].done, g)
			continue
		}
		child := top.rest[0]
		top.rest = top.rest[1:]
		cv := child.Value.Resolve()
		if cv.Kind() == xslog.KindGroup && len(stack) < convAttrMaxDepth {
			stack = append(stack, convFrame{key: child.Key, rest: cv.Group(), done: make([]any, 0, len(cv.Group()))})
			continue
		}
		top.done = append(top.done, slog.Attr{Key: child.Key, Value: slog.AnyValue(cv.Any())})
	}
	return slog.Group(stack[0].key, stack[0].done...)
}
