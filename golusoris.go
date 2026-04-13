// Package golusoris is the top-level entry: it re-exports composable [fx.Module]
// groupings so apps can compose only what they need.
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.DB,
//	    golusoris.HTTP,
//	).Run()
//
// Subpackages provide the actual implementations. The groupings here just
// bundle commonly-used sets so app wiring stays terse.
package golusoris

import (
	"go.uber.org/fx"

	cachemem "github.com/golusoris/golusoris/cache/memory"
	cacheredis "github.com/golusoris/golusoris/cache/redis"
	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/crypto"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/httpx/router"
	"github.com/golusoris/golusoris/httpx/server"
	"github.com/golusoris/golusoris/id"
	"github.com/golusoris/golusoris/jobs"
	k8sclient "github.com/golusoris/golusoris/k8s/client"
	"github.com/golusoris/golusoris/k8s/podinfo"
	"github.com/golusoris/golusoris/log"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/validate"
)

// Core bundles the foundational modules every app needs:
// config, log, clock, id, validate, crypto.
//
// errors/ and i18n/ are intentionally not in fx — errors is a pure package and
// i18n is opt-in via [I18n].
var Core = fx.Module("golusoris.core",
	config.Module,
	log.Module,
	clock.Module,
	id.Module,
	validate.Module,
	crypto.Module,
)

// DB bundles the database modules: pgx pool + golang-migrate runner.
// Requires [Core] in the same fx graph for config, log, and clock.
//
// db/sqlc helpers are stateless (no fx wiring), so they're available via
// direct import without inclusion here.
var DB = fx.Module("golusoris.db",
	dbpgx.Module,
	dbmigrate.Module,
)

// HTTP bundles the base HTTP stack: chi router + *http.Server with
// slow-loris guards, body limits, and graceful shutdown. Apps add
// middleware via fx.Invoke against the provided chi.Router.
//
// Individual httpx/middleware functions are not in fx (they're plain
// net/http middleware); apps compose the stack they want and register it
// via router.Use.
var HTTP = fx.Module("golusoris.http",
	router.Module,
	server.Module,
)

// K8s bundles the Kubernetes runtime modules: podinfo (downward-API env
// → typed PodInfo) + client (rest.Config + clientset, in-cluster or
// kubeconfig).
//
// Health (k8s/health) and metrics (k8s/metrics/prom) aren't in this
// umbrella because they take a Registry argument that doesn't fit a
// generic provider — apps wire them with their own fx.Invoke.
//
// Leader election lives under top-level [leader/] (k8s-Lease or
// Postgres advisory lock) so non-k8s apps can elect too. Runtime-
// agnostic identity lives under [container/runtime] — prefer it in
// new code.
var K8s = fx.Module("golusoris.k8s",
	podinfo.Module,
	k8sclient.Module,
)

// Jobs bundles the background-job modules: the river client + a
// Workers registry. Apps register workers via fx.Invoke(func(w
// *jobs.Workers) { jobs.Register(w, &MyWorker{}) }).
//
// Requires [Core] + [DB] in the same fx graph (river needs a pg pool).
//
// jobs/cron (periodic helpers) and jobs/ui (admin dashboard) are not
// in this umbrella — apps wire them explicitly against the Client.
var Jobs = fx.Module("golusoris.jobs",
	jobs.Module,
)

// Outbox bundles the transactional-outbox drainer. Apps must supply a
// Dispatcher via fx.Supply or fx.Provide and run under a leader
// (leader/k8s or leader/pg) so only one replica drains.
//
// Requires [Core] + [DB] + [Jobs] in the same fx graph. The outbox
// schema lives in outbox/migrations/ — wire via dbmigrate.Options{}.
// WithFS(outbox.MigrationsFS) or copy the SQL into the app's own
// migrations directory.
var Outbox = fx.Module("golusoris.outbox",
	outbox.Module,
)

// CacheMemory bundles the in-process L1 cache (otter). Provides
// *memory.Cache to the fx graph. Standalone — does not require DB.
//
// Requires [Core] for config + log.
var CacheMemory = fx.Module("golusoris.cache.memory",
	cachemem.Module,
)

// CacheRedis bundles the Redis client (rueidis). Provides
// rueidis.Client to the fx graph. Auto-detects standalone vs cluster.
//
// Requires [Core] for config + log. Redis must be reachable at start.
var CacheRedis = fx.Module("golusoris.cache.redis",
	cacheredis.Module,
)
