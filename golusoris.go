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

	aillm "github.com/golusoris/golusoris/ai/llm"
	aivector "github.com/golusoris/golusoris/ai/vector"
	"github.com/golusoris/golusoris/audit"
	"github.com/golusoris/golusoris/auth/oidc"
	"github.com/golusoris/golusoris/authz"
	cachemem "github.com/golusoris/golusoris/cache/memory"
	cacheredis "github.com/golusoris/golusoris/cache/redis"
	cachetwotier "github.com/golusoris/golusoris/cache/twotier"
	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/crypto"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/flags"
	extclient "github.com/golusoris/golusoris/httpx/extclient"
	"github.com/golusoris/golusoris/httpx/router"
	"github.com/golusoris/golusoris/httpx/server"
	"github.com/golusoris/golusoris/id"
	"github.com/golusoris/golusoris/idempotency"
	"github.com/golusoris/golusoris/jobs"
	k8sclient "github.com/golusoris/golusoris/k8s/client"
	"github.com/golusoris/golusoris/k8s/podinfo"
	"github.com/golusoris/golusoris/log"
	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/search"
	"github.com/golusoris/golusoris/secrets"
	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/tenancy"
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

// CacheTwoTier bundles the read-through two-tier cache: L1 in-process (otter)
// + L2 redis, with single-flight load coalescing. Provides *twotier.TwoTier.
//
// Requires [CacheMemory] + [CacheRedis] in the same fx graph.
var CacheTwoTier = fx.Module("golusoris.cache.twotier",
	cachetwotier.Module,
)

// AuthOIDC bundles the OIDC + OAuth2 PKCE client. Provides
// *oidc.Provider to the fx graph after running OIDC discovery against
// the configured issuer.
//
// Requires [Core] for config + log. Config key prefix: auth.oidc.*.
var AuthOIDC = fx.Module("golusoris.auth.oidc",
	oidc.Module,
)

// Authz bundles the Casbin policy enforcer. Provides *authz.Enforcer.
// Apps must supply authz.Options (model + adapter) via fx.Supply or
// fx.Provide before including this module.
//
// Requires [Core] for log.
var Authz = fx.Module("golusoris.authz",
	authz.Module,
)

// Storage bundles the object-storage Bucket. Provides storage.Bucket
// (local-filesystem backend by default; S3/GCS via config when added).
//
// Requires [Core] for config + log. Config key prefix: storage.*.
var Storage = fx.Module("golusoris.storage",
	storage.Module,
)

// Secrets bundles the secrets provider. Provides secrets.Secret
// (env backend by default; file backend via config).
//
// Requires [Core] for config + log. Config key prefix: secrets.*.
var Secrets = fx.Module("golusoris.secrets",
	secrets.Module,
)

// Flags bundles the feature-flag client. Provides flags.Provider +
// *flags.Client (noop provider by default; memory via config).
//
// Requires [Core] for config + log. Config key prefix: flags.*.
var Flags = fx.Module("golusoris.flags",
	flags.Module,
)

// Audit bundles the append-only audit logger. Provides *audit.Logger
// backed by a MemoryStore (apps override the Store via fx.Decorate).
//
// Requires [Core] for clock + log. Config key prefix: audit.*.
var Audit = fx.Module("golusoris.audit",
	audit.Module,
)

// Tenancy bundles the multi-tenant resolution middleware. Default
// extractor + MemoryStore from config (apps override the Store).
//
// Requires [Core] for config + log. Config key prefix: tenancy.*.
var Tenancy = fx.Module("golusoris.tenancy",
	tenancy.Module,
)

// Idempotency bundles the Idempotency-Key middleware + Store
// (MemoryStore by default; apps override via fx.Decorate).
//
// Requires [Core] for config + clock. Config key prefix: idempotency.*.
var Idempotency = fx.Module("golusoris.idempotency",
	idempotency.Module,
)

// Search bundles the search Backend. Provides search.Backend
// (in-memory by default; typesense/meilisearch via config).
//
// Requires [Core] for config + log. Config key prefix: search.*.
var Search = fx.Module("golusoris.search",
	search.Module,
)

// Notify bundles the Notifier. Provides *notify.Notifier with the SMTP
// sender by default; additional senders selectable by config.
//
// Requires [Core] for config + log. Config key prefix: notify.*.
var Notify = fx.Module("golusoris.notify",
	notify.Module,
)

// AILLM bundles an OpenAI-compatible LLM client. Provides ai/llm.Client
// (works with OpenAI, Azure OpenAI, Ollama, Groq, Mistral, LM Studio).
//
// Requires [Core] for config. Config key prefix: ai.llm.*.
var AILLM = fx.Module("golusoris.ai.llm",
	aillm.Module,
)

// AIVector registers pgvector types on the pgx pool at startup so vector
// columns scan/encode correctly. Provides no new type — configures the pool.
//
// Requires [DB] (a *pgxpool.Pool) in the same fx graph.
var AIVector = fx.Module("golusoris.ai.vector",
	aivector.Module,
)

// ExtClient bundles the typed external-API client factory (per-host retry +
// breaker + OTel + optional cache, built on httpx/client). Provides the
// extclient registry; apps resolve named services from config.
//
// Requires [Core] for config + log. Config key prefix: httpx.extclient.*.
var ExtClient = fx.Module("golusoris.httpx.extclient",
	extclient.Module,
)
