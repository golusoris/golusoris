<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — integrations/goenvoy/

Thin fx adapter that wires opt-in [goenvoy](https://github.com/golusoris/goenvoy)
service clients (Sonarr, TMDb, AniList, Trakt.) onto framework's
resilient outbound `*http.Client` (`httpx/client`: retry + circuit-breaker +
otelhttp + slog). **It does NOT reimplement goenvoy** — it only injects framework transport, optional response cache, and config into goenvoy's own
clients.

## API

```go
fx.New(
    golusoris.Core,
    memory.Module,   // optional: enables the HTTP response cache
    clock.Module,    // optional: deterministic cache TTL (real clock if absent)
    goenvoy.Module,  // provides *goenvoy.Registry
    fx.Invoke(func(r *goenvoy.Registry) error {
        sonarr, err := r.Sonarr("sonarr")  // *sonarr.Client
        tmdb,   err := r.TMDb("tmdb")       // *tmdb.Client
        ani,    err := r.AniList("anilist") // *anilist.Client
        trakt,  err := r.Trakt("trakt")     // *trakt.Client
        ...
    }),
)
```

`*Registry` builds each goenvoy client lazily on first lookup over its **own**
`*http.Client`, then caches it. `Names()` lists configured services.

## Config (env: `APP_INTEGRATIONS_GOENVOY_*`)

```yaml
integrations:
  goenvoy:
    services:
      sonarr: { provider: sonarr, base_url: http://sonarr:8989, api_key: ${SONARR_API_KEY}, timeout: 10s, cache_ttl: 30s, retry: { max: 3 } }
      tmdb:   { provider: tmdb,   access_token: ${TMDB_V4_TOKEN}, cache_ttl: 5m }
      anilist:{ provider: anilist }
      trakt:  { provider: trakt,  client_id: ${TRAKT_ID}, client_secret: ${TRAKT_SECRET}, access_token: ${TRAKT_TOKEN} }
```

Leaf keys contain underscores (`base_url`, `api_key`, `access_token`.). env loader splits on `_`, so to override leaf from env var declare it as `config.Options.CompoundKeys` entry (e.g. for secret interpolation of
`api_key`). YAML/JSON files keep keys verbatim.

## Why goenvoy (and why an adapter, not a reimpl)

- **Mandated upstream** + near-ideal adapter target: zero external transitive
 deps, MIT, every client takes `WithHTTPClient(*http.Client)` — exact seam
 to inject framework's resilient client uniformly across arr + metadata.
- Alternatives rejected: `starr` (arr-only, no tmdb/anilist/trakt; two
 ecosystems); per-provider libs (4+ unrelated http-injection stories);
 reimplementing on `httpx/extclient` generics (task says wire, don't reimpl).

## Notes

- **One `*http.Client` per service — never shared.** goenvoy's
 `arr.WithTimeout` / `metadata.WithTimeout` mutate `httpClient.Timeout` *in
 place*; shared transport would corrupt timeouts. Timeout is therefore set
 via `client.Options.Timeout`, and goenvoy's `WithTimeout` is never passed.
- **HTTP cache** (`cache.go`): `RoundTripper` over `cache/memory`, GET-only,
 2xx-only, TTL driven by injected `clock.Clock` (deterministic in tests). Keys
 include credential header (`Authorization` / `X-Api-Key` /
 `Trakt-Api-Key`) so response is never served across distinct credentials.
 Disabled when `cache_ttl == 0` or no `memory.Cache` is wired.
- **Credentials** (`api_key` / `access_token` / `client_secret`) flow through
 config — resolve via framework's `secrets/` interpolation and keep them
 out of slog lines (`started` Debug line logs only counts + cache bool).
- **Trakt OAuth**: `SetClientSecret` / `SetAccessToken` are applied at build.
 Refresh-token rotation/persistence is app's responsibility — this thin
 adapter does not persist rotated tokens.
- `OnStop` calls `CloseIdleConnections()` on every built client. No `init()` —
 all construction is in fx constructors.

## Don't

- Don't create any bare `*http.Client` without `Timeout`, and never use `http.DefaultClient` — all outbound HTTP must flow through parent `httpx` / `extclient` package (which sets timeout, retry, circuit-breaker, and OTel).

## Version caveat (flag to maintainers)

`metadata/anime/anilist` remains pinned to the reviewed pseudo-version
`v1.3.1-0.20260906000323-66546eaf9315`; pin it to its own tag once cut.
Sonarr, TMDb, and Trakt use `v1.3.1` tags. Category modules use `arr/v2`
`v2.1.0` and `metadata` `v1.3.1`; keep `golusoris/...` import paths.
