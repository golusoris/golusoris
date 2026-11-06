// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command minimal demonstrates a minimal golusoris app composing five modules:
// Core (config + log + clock + id), DB (pgx + migrate), HTTP (server + router),
// OTel (tracer + meter), and K8s runtime metadata/client wiring. It serves
// /livez, /readyz and /startupz; /readyz reports the Postgres ping.
//
// Run:
//
//	export APP_HTTP_ADDR=":8080"
//	export APP_DB_DSN="postgres://..."
//	export APP_K8S_KUBECONFIG="/path/to/kubeconfig" # outside a cluster; in a pod the in-cluster config is used
//	go run github.com/golusoris/golusoris/examples/minimal
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
