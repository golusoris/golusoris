// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
		golusoris.DB,          // pgx pool + migrations
		otel.Module,           // tracer + meter + OTLP
		golusoris.HTTP,        // chi router + HTTP server
		golusoris.K8s,         // pod metadata + Kubernetes client
		golusoris.Jobs,        // river client + worker registry
		golusoris.CacheMemory, // otter L1 cache
		golusoris.CacheRedis,  // rueidis L2 cache
		// ── Auth + authz ──────────────────────────────────────────────────────
		golusoris.AuthOIDC, // PKCE OIDC
		authz.Module,       // Casbin RBAC
		// ── Commerce ──────────────────────────────────────────────────────────
		stripe.Module, // Stripe checkout + portal + intents
	).Run()
}
