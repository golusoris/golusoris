// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command minimal demonstrates a minimal golusoris app composing five modules:
// Core (config + log + clock + id), DB (pgx + migrate), HTTP (server + router),
// OTel (tracer + meter), and K8s runtime metadata/client wiring.
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
		golusoris.Core, // config + log + clock + id + validate + crypto
		golusoris.DB,   // pgx pool + migrations
		otel.Module,    // tracer + meter + logs + OTLP
		golusoris.HTTP, // chi router + HTTP server
		golusoris.K8s,  // pod metadata + Kubernetes client
	).Run()
}
