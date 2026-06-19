# ADR-0010: goenvoy multi-module clients wired onto the framework's resilient HTTP stack

- **Status**: Accepted
- **Date**: 2026-06-19
- **Deciders**: lusoris
- **Tags**: integrations, http, metadata, arr

## Context

The framework needs typed clients for media-automation upstreams: the *arr stack (Sonarr/Radarr/...) and metadata providers (TMDb, AniList, Trakt). goenvoy is the mandated upstream — a multi-module monorepo where each service is its own Go module with zero external transitive deps and a uniform `WithHTTPClient(*http.Client)` injection seam. The framework already ships a resilient outbound client (`httpx/client`: circuit-breaker -> retry -> otelhttp -> slog) and an optional in-process cache (`cache/memory`); per principles.md §2 (Power-of-10 error wrapping, SEI CERT credential handling, OTel SemConv observability) every outbound call should ride that resilient, instrumented transport rather than goenvoy's default zero-value `http.Client`.

## Decision

We will ship `integrations/goenvoy` as a thin fx adapter that injects the framework's resilient `*http.Client`, optional response cache, clock, and config into goenvoy's own clients, and provide them via a `*Registry` keyed by configured service name. We will NOT reimplement goenvoy's typed surface. Each service receives its own dedicated `*http.Client` (timeout set via `client.Options`), and goenvoy's in-place-mutating `WithTimeout` is never used.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| goenvoy thin adapter (chosen) | One uniform injection/observability story across arr+tmdb+anilist+trakt; zero new third-party deps; MIT; ctx-first, error-wrapping clients match house style | Multi-module monorepo means N versioned `require`s; per-service subpackages only on HEAD pseudo-versions until tags cut | — |
| starr (golift.io/starr) | Mature, well-maintained arr client | arr-only — no tmdb/anilist/trakt; own http.Client config, not clean WithHTTPClient injection | Abandons metadata providers; two client ecosystems; task is a goenvoy adapter |
| Per-provider best-of-breed libs (go-tmdb, go-anidb, ...) | Each may be strong in isolation | 4+ unrelated libs, each a different maintenance/http-injection story | Defeats the point of one thin adapter |
| Reimplement on httpx/extclient generics | Reuses framework typed-client path | Re-implements goenvoy's hundreds of endpoints + response structs | Task explicitly says wire, not reimplement |

## Consequences

- **Positive**: a single resilient/observable transport across all media upstreams; opt-in per service via config; no new third-party transitive deps; the in-place-timeout-mutation hazard is structurally avoided (one client per service).
- **Negative**: each opted-in service adds a separately versioned goenvoy module `require`; an HTTP response cache (credential-aware caching RoundTripper) had to be added because `httpx/client` has no response cache and goenvoy bypasses extclient's JSON-layer cache.
- **Neutral / follow-ups**: service subpackages are pinned to HEAD pseudo-versions (`v1.3.1-0.20260619...`) until per-service tags are cut — pin to tags then. Resolve the `LUSORIS` vs `golusoris` import-path mismatch on the old `arr@v1.x` tag before pinning. Trakt OAuth refresh-token rotation/persistence is out of scope (app responsibility). Run a central `go mod tidy` to promote the four goenvoy modules from indirect to direct.

## References

- goenvoy: github.com/golusoris/goenvoy (multi-module monorepo, MIT)
- ADR-0001 (fx for DI), `httpx/client` (resilient outbound stack), `cache/memory`
- principles.md §2 (Power-of-10, SEI CERT, OTel SemConv)