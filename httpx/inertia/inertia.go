// Package inertia wires the gonertia Inertia.js v2 server adapter into the fx
// graph. It provides a *inertia.Inertia handle apps inject into handlers to
// render server-driven SPA pages over chi/net/http.
//
// Apps mount the middleware on their router and render components from
// handlers; this module never registers routes itself:
//
//	fx.New(
//	    golusoris.Core,
//	    inertia.Module,                 // provides *inertia.Inertia
//	    fx.Supply(inertia.RootFS{FS: webFS}), // optional: app's embed.FS
//	    fx.Invoke(func(i *inertia.Inertia, r chi.Router) {
//	        r.Use(i.Middleware)
//	        r.Get("/", func(w http.ResponseWriter, req *http.Request) {
//	            _ = i.Render(w, req, "Dashboard", inertia.Props{"user": u})
//	        })
//	    }),
//	)
//
// The root template (default web/root.html) must contain the {{ .inertia }}
// and {{ .inertiaHead }} placeholders and load the matching @inertiajs client
// bundle, or the browser renders a blank page. See AGENTS.md.
package inertia

import (
	"fmt"
	"log/slog"
	"strings"

	gonertia "github.com/romsar/gonertia/v3"
)

// Inertia is the render/middleware handle apps depend on. Re-exported so
// handlers call i.Render(w, r, component, props) without importing gonertia.
type Inertia = gonertia.Inertia

// Props is the per-page prop map passed to Render. Re-exported so apps don't
// import gonertia directly.
type Props = gonertia.Props

// slogLogger adapts gonertia's Printf/Println Logger to *slog.Logger so the
// adapter's debug output flows through the framework's slog handler instead of
// the stdlib log package (which would violate the no-fmt.Println rule).
type slogLogger struct{ l *slog.Logger }

// Printf forwards a formatted gonertia message to slog at debug level.
func (s slogLogger) Printf(format string, v ...any) {
	s.l.Debug(fmt.Sprintf(format, v...))
}

// Println forwards a gonertia message to slog at debug level (space-separated,
// trailing newline trimmed to keep the slog record on one line).
func (s slogLogger) Println(v ...any) {
	s.l.Debug(strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
}
