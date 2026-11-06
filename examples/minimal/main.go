// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command minimal composes Core, DB, HTTP, OTel and K8s and serves /livez, /readyz and /startupz.
// It needs APP_DB_DSN and, outside a cluster, APP_K8S_KUBECONFIG; run it with `go run ./examples/minimal`.
package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
	"github.com/golusoris/golusoris/otel"
)

func main() {
	app().Run()
}

// app composes the module graph; the sizing test builds and runs this binary.
func app() *fx.App {
	return fx.New(
		golusoris.Core, // config + log + clock + id + validate + crypto
		golusoris.DB,   // pgx pool + migrations
		otel.Module,    // tracer + meter + logs + OTLP
		golusoris.HTTP, // chi router + HTTP server
		golusoris.K8s,  // pod metadata + Kubernetes client

		fx.Provide(statuspage.NewRegistry), // shared probe registry
		health.Module,                      // drain gate behind /readyz
		dbpgx.ReadinessModule,              // /readyz fails while Postgres is down
		fx.Invoke(mountHealth),             // /livez /readyz /startupz on the router
		fx.Invoke(func(*http.Server) {}),   // consume the server so its listen hook registers
	)
}

func mountHealth(r chi.Router, reg *statuspage.Registry) {
	health.Mount(r, reg)
}
