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

// convAttr converts an x/exp/slog Attr to a stdlib one, recursing into groups.
func convAttr(a xslog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() == xslog.KindGroup {
		group := v.Group()
		conv := make([]any, 0, len(group))
		for _, ga := range group {
			conv = append(conv, convAttr(ga))
		}
		return slog.Group(a.Key, conv...)
	}
	return slog.Attr{Key: a.Key, Value: slog.AnyValue(v.Any())}
}
