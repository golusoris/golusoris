// Command minimal demonstrates a minimal golusoris app composing five modules:
// Core (config + log + clock + id), DB (pgx + migrate), HTTP (server + router),
// OTel (tracer + meter), and K8s health probes (/livez /readyz /startupz).
//
// Run:
//
//	export APP_HTTP_ADDR=":8080"
//	export APP_DB_DSN="postgres://..."
//	go run github.com/golusoris/golusoris/examples/minimal
package main

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris"
	"github.com/golusoris/golusoris/otel"
)

func main() {
	fx.New(
		golusoris.Core,  // config + log + lifecycle + errors + clock + id
		golusoris.DB,    // pgx pool + migrations + sqlc helpers
		otel.Module,     // tracer + meter + logs + OTLP
		golusoris.HTTP,  // server + standard middleware + Scalar docs
		golusoris.K8s,   // /livez /readyz /startupz + podinfo + prom /metrics
	).Run()
}
