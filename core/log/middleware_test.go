// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/log"
)

// tagHandler appends its tag to the "chain" attribute so tests can read the
// wrap order back from the JSON record.
type tagHandler struct {
	slog.Handler

	tag string
}

func (h tagHandler) Handle(ctx context.Context, r slog.Record) error {
	chain := h.tag
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "chain" {
			chain = a.Value.String() + ">" + h.tag
			return false
		}
		return true
	})
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	out.AddAttrs(slog.String("chain", chain))
	return h.Handler.Handle(ctx, out)
}

func (h tagHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return tagHandler{Handler: h.Handler.WithAttrs(attrs), tag: h.tag}
}

func (h tagHandler) WithGroup(name string) slog.Handler {
	return tagHandler{Handler: h.Handler.WithGroup(name), tag: h.tag}
}

func tagMiddleware(name string, order int) log.HandlerMiddleware {
	return log.HandlerMiddleware{Name: name, Order: order, Wrap: func(next slog.Handler) slog.Handler {
		return tagHandler{Handler: next, tag: name}
	}}
}

func chainOf(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output not JSON: %v (%s)", err, buf.String())
	}
	chain, _ := rec["chain"].(string)
	return chain
}

func TestNewAppliesMiddlewareByOrderThenName(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := log.New(log.Options{Format: log.FormatJSON, Output: &buf, Middleware: []log.HandlerMiddleware{
		tagMiddleware("outer", 10),
		tagMiddleware("b-inner", 0),
		tagMiddleware("a-inner", 0),
	}})
	l.Info("hello")
	// Outermost handles first, so the chain lists outer -> inner.
	if got, want := chainOf(t, &buf), "outer>b-inner>a-inner"; got != want {
		t.Fatalf("chain = %q, want %q", got, want)
	}
}

func TestNewSkipsNilMiddleware(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := log.New(log.Options{Format: log.FormatJSON, Output: &buf, Middleware: []log.HandlerMiddleware{
		{Name: "disabled"},
		{Name: "returns-nil", Wrap: func(slog.Handler) slog.Handler { return nil }},
		tagMiddleware("only", 0),
	}})
	l.Info("hello")
	if got := chainOf(t, &buf); got != "only" {
		t.Fatalf("chain = %q, want only", got)
	}
}

func TestNewWithoutMiddlewareKeepsBaseHandler(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log.New(log.Options{Format: log.FormatJSON, Output: &buf}).Info("hello")
	if strings.Contains(buf.String(), "chain") {
		t.Fatalf("unexpected decoration: %s", buf.String())
	}
}

func TestModuleReadsMiddlewareGroupFromOtherModules(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("APP_LOG_FORMAT", "json")

	var logger *slog.Logger
	app := fxtest.New(t,
		fx.Provide(func() (*config.Config, error) { return config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."}) }),
		log.Module,
		fx.Module("contributor", fx.Provide(log.AsMiddleware(func() log.HandlerMiddleware {
			return tagMiddleware("contributed", 0)
		}))),
		fx.Populate(&logger),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	got := logger.Handler()
	if _, ok := got.(tagHandler); !ok {
		t.Fatalf("injected logger handler = %T, want the contributed middleware outermost", got)
	}
}
