// Command full demonstrates a production-ready golusoris app composing the
// major modules. Copy and remove the modules you don't need.
//
// Required config (koanf / env vars with APP_ prefix):
//
//	APP_DB_DSN             — Postgres DSN
//	APP_HTTP_ADDR          — listen address (default :8080)
//	APP_CACHE_REDIS_ADDR   — Redis address (default localhost:6379)
package main

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris"
	"github.com/golusoris/golusoris/authz"
	"github.com/golusoris/golusoris/otel"
	"github.com/golusoris/golusoris/payments/stripe"
)

func main() {
	fx.New(
		// ── Core ──────────────────────────────────────────────────────────────
		golusoris.Core,        // config + log + clock + id + errors + validate + crypto
		golusoris.DB,          // pgx pool + migrations + sqlc
		otel.Module,           // tracer + meter + OTLP
		golusoris.HTTP,        // server + middleware + Scalar docs
		golusoris.K8s,         // /livez /readyz /startupz + /metrics
		golusoris.Jobs,        // river queue + cron
		golusoris.CacheMemory, // otter L1 cache
		golusoris.CacheRedis,  // rueidis L2 cache
		// ── Auth + authz ──────────────────────────────────────────────────────
		golusoris.AuthOIDC, // PKCE OIDC
		authz.Module,       // Casbin RBAC
		// ── Commerce ──────────────────────────────────────────────────────────
		stripe.Module, // Stripe checkout + portal + intents
	).Run()
}
