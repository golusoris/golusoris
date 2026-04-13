# Agent guide — golusoris

> Cross-tool agent context for [Claude Code](https://claude.com/claude-code), [Cursor](https://cursor.sh), [Aider](https://aider.chat), [Codex](https://github.com/openai/codex), [Continue](https://continue.dev), and other coding assistants. **Read this first** before suggesting changes.

## What this repo is

`golusoris` is a single-module Go framework (`github.com/golusoris/golusoris`) wrapping a pinned set of best-in-class libraries behind opt-in `go.uber.org/fx` modules. Apps compose only the modules they need.

## Hard rules

1. **Never break public API without a `Migration:` footer.** CI runs `apidiff` against the previous tag and fails otherwise.
2. **Never add a new transitive dependency** without weighing it against awesome-go alternatives. Frame the choice in a PR comment if non-obvious.
3. **Every subpackage exposes its capability as `fx.Module` or `fx.Options`.** Apps never import internals directly — they import the `fx.Module`.
4. **No init() side effects.** All wiring happens through fx lifecycle hooks.
5. **All errors flow through `golusoris/errors`** (or wrap go-faster/errors directly with the same conventions).
6. **All time uses `golusoris/clock`.** `time.Now()` is banned outside the clock package — it breaks tests.
7. **Logs go through the slog handler from `golusoris/log`.** No `fmt.Println`, no global loggers.
8. **Use `Deprecated:` doc comments** for soft-removal. staticcheck SA1019 surfaces uses.

## Layout

```
golusoris/
├── golusoris.go              # top-level fx.Module re-exports (Core, DB, HTTP, ...)
├── config/  log/  errors/  crypto/  clock/  id/  validate/  i18n/   # core         [landed]
├── db/                                                                # db layer     [landed]
│   ├── pgx/  migrate/  sqlc/
│   ├── geo/                                                           #   PostGIS point helpers [landed]
│   ├── timescale/                                                     #   TimescaleDB helpers   [landed]
│   └── clickhouse/                                                    #   ClickHouse OLAP client [landed]
├── httpx/                                                             # HTTP stack   [landed]
│   ├── server/  router/  middleware/  client/
│   ├── form/  htmx/  vite/  static/  static/hashfs/
│   ├── cors/  csrf/  ratelimit/  geofence/
│   ├── ws/  autotls/  autotls/autocert/  autotls/certmagic/
├── ogenkit/  apidocs/                                                 # ogen + docs  [landed]
├── otel/  observability/                                              # obs          [landed]
│   ├── observability/sentry/  profiling/  pprof/  statuspage/
├── container/                                                         # runtime      [landed]
│   └── runtime/                                                       #   k8s/docker/podman/systemd/bare identity
├── leader/                                                            # election     [landed]
│   ├── k8s/  pg/                                                      #   Lease or pg advisory lock
├── systemd/                                                           # sd_notify    [landed]
├── k8s/                                                               # k8s          [landed]
│   ├── podinfo/  health/  metrics/prom/  client/
├── jobs/                                                              # background   [landed]
│   ├── cron/  ui/
├── outbox/                                                            # tx outbox    [landed]
├── cache/                                                             # caching      [landed]
│   ├── memory/  redis/  singleflight/
├── auth/                                                              # auth         [partial: jwt/apikey/oidc/session landed]
│   ├── jwt/  apikey/  oidc/  session/
├── authz/                                                             # authorization [landed]
├── notify/                                                            # notifications [partial: smtp/unsub landed]
│   └── unsub/
├── realtime/                                                          # realtime      [partial: sse/pubsub landed]
│   ├── sse/  pubsub/
├── webhooks/                                                          # webhooks      [landed]
│   ├── in/   out/
├── tenancy/  idempotency/  flags/  audit/  page/                      # SaaS          [landed]
├── storage/                                                           # object store  [landed]
├── hash/  markdown/  archive/                                         # files/data   [landed]
│   ├── httpx/rangeserve/   fs/watch/
├── media/  ocr/  pdf/  htmltmpl/  jsonschema/                        # files/data   [planned]
├── search/                                                            # search        [landed]
├── ai/                                                                # AI            [landed]
│   ├── llm/   vector/
├── payments/                                                          # commerce      [partial]
│   ├── stripe/
├── money/                                                             # money type    [landed]
├── geoip/                                                             # geoip         [landed]
├── secrets/                                                           # secrets        [landed]
├── integrations/  torrent/                                            # integrations  [planned]
├── clikit/                                                            # CLI           [landed]
│   └── tui/                                                           #   bubbletea helpers [landed]
├── selfupdate/                                                        # self-update    [landed]
├── pubsub/                                                            # pub/sub       [partial]
│   ├── kafka/                                                         #   franz-go Kafka [landed]
│   └── nats/                                                          #   NATS JetStream [landed]
├── plugin/  ebpf/                                                     # misc          [planned]
├── testutil/                                                          # test helpers [partial]
│   ├── pg/  redis/  river/                                            #   containers + river harness [landed]
│   ├── fxtest/                                                        #   fx lifecycle test helper [landed]
│   ├── factory/                                                       #   gofakeit deterministic factory [landed]
│   └── snapshot/                                                      #   go-snaps snapshot testing [landed]
├── cmd/                                                               # binaries     [landed]
│   ├── golusoris/                                                     #   scaffolder CLI (init/add/bump) [landed]
│   └── golusoris-mcp/                                                 #   MCP server for framework tools [landed]
├── deploy/                                                            # deploy       [partial]
│   ├── helm/                                                          #   base Helm chart [landed]
│   └── observability/                                                 #   PrometheusRule + Grafana dashboard [landed]
├── tools/                                                             # shared configs
├── template/.github/  template/.devcontainer/                         # repo template
├── docs/
│   ├── adr/                                                           # ADRs (Nygard) [0001-0007 backfilled]
│   ├── architecture/                                                  # C4 PlantUML L1+L2 diagrams
│   ├── upstream/                                                      # pinned upstream docs
│   └── migrations/                                                    # per-version migration guides
└── AGENTS.md  CLAUDE.md                                               # this file + Claude-specific
```

Per-subpackage `AGENTS.md` files give package-level conventions, idioms, and pinned doc URLs.

## Common tasks

| Task | Command / Skill |
|---|---|
| Add a new fx module | `wire-fx-module` skill — see `.claude/skills/wire-fx-module/` |
| Add an ogen handler | `scaffold-ogen-handler` skill |
| Add a river worker | `add-river-worker` skill |
| Add a DB migration | `add-migration` skill |
| Bump golusoris in a downstream app | `bump-golusoris` skill or `golusoris bump <version>` CLI |

## Pinned upstream docs

Version-pinned snapshots live in `docs/upstream/`. Read those before suggesting API patterns — the public docs may be ahead/behind our pinned versions.

| Package | Pinned doc |
|---|---|
| `go.uber.org/fx` v1.24.0 | `docs/upstream/fx/` |
| `jackc/pgx/v5` v5.9.1 | `docs/upstream/pgx/` |
| `ogen-go/ogen` v1.20.3 | `docs/upstream/ogen/` |
| `riverqueue/river` v0.34.0 | `docs/upstream/river/` |
| `knadh/koanf/v2` v2.3.4 | `docs/upstream/koanf/` |
| ... | ... |

## CI gates

Every PR must pass:
- golangci-lint (full set: govet, staticcheck, gosec, govulncheck, gocritic, revive, gocyclo, funlen, gocognit, bodyclose, rowserrcheck, sqlclosecheck, errorlint, wrapcheck, gci, gofumpt, misspell, godot, whitespace)
- govulncheck
- `go test -race -count=1` + coverage
- apidiff vs previous tag (no undeclared breaking changes)
- Conventional-commit PR title

## When in doubt

Read [.workingdir/PLAN.md](.workingdir/PLAN.md) (when present) for full scope and decisions log. Then read the per-subpackage `AGENTS.md` for the area you're touching.
