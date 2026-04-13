// Package router provides a [chi.Router] as an fx dependency. Apps mount
// routes via fx.Invoke(func(r chi.Router) { r.Get("/foo", ...) }).
//
// The same router is exposed as a [http.Handler] so [httpx/server.Module]
// picks it up automatically.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

// New returns a fresh chi.Mux. Most apps use [Module] instead.
func New() *chi.Mux { return chi.NewRouter() }

// Module provides a *chi.Mux (as both chi.Router and http.Handler) so apps
// can inject either interface. Mount routes via fx.Invoke.
var Module = fx.Module("golusoris.httpx.router",
	fx.Provide(
		New,
		func(m *chi.Mux) chi.Router { return m },
		func(m *chi.Mux) http.Handler { return m },
	),
)
