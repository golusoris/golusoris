<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0028: Outbound HTTP response cache as a transport on bartventer/httpcache and cache/twotier

- **Status**: Accepted
- **Date**: 2026-11-13
- **Deciders**: @lusoris
- **Tags**: `http`, `cache`, `security`, `dependencies`

## Context

Applications that proxy third-party metadata APIs (TVDB, TMDB, Plex and similar) need a shared response cache in front of those providers. The cache has to honour HTTP caching headers, let an operator override freshness per host, keep serving a stored response when the provider fails, and keep secrets out of storage. The cache key must not contain API keys, PINs or bearer tokens, and the cache must never store login responses or bodies that carry credentials. Provider quotas also need a per-host outbound rate limit. Replicas must share the cache, and the operator's standard key-value stack is rueidis as the client with Dragonfly or Redis as the server.

The repository already has pieces of this:

- `httpx/extclient` caches GET responses in process memory. Its key includes a hash of every request header, credentials included, and it ignores `Cache-Control`.
- `cache/twotier` puts in-process otter (L1) in front of rueidis (L2), with singleflight on loads and epoch-fenced writes.
- `httpx/ratelimit` limits inbound requests per peer IP with `ulule/limiter/v3`.
- `httpx/client` lets callers replace the innermost transport through `Options.Transport`.

Writing RFC 9111 freshness, validation, `Vary` handling and RFC 5861 stale serving by hand would repeat a library's work. We compared these options for the RFC logic. awesome-go lists no HTTP-client cache transport in any category, so we also checked the Go module index and GitHub:

| Candidate | Finding |
| :--- | :--- |
| `github.com/bartventer/httpcache` v0.14.0 | RFC 9111 private cache with RFC 5861 `stale-if-error` and `stale-while-revalidate` and RFC 8246 `immutable`. No module dependencies; Apache-2.0; pluggable storage driver. |
| `github.com/gregjones/httpcache` | Archived in 2023; RFC 7234 only; no `stale-if-error`. |
| `github.com/sandrolain/httpcache` | Fork of gregjones/httpcache with the same RFC 7234 core. |
| `github.com/pquerna/cachecontrol` | Only decides whether a response is storable (RFC 7234). Storage, validation and stale serving would still be hand-written. |
| `github.com/darkweak/souin` | Server-side reverse-proxy cache and middleware, not a client transport. |

bartventer/httpcache v0.14.0 has these properties, read from its source (`roundtripper.go`, `store/cache.go`, `store/driver/driver.go`, `internal/entry.go`, `internal/helpers.go`):

- Storage drivers register globally through `store.Register(name, driver)`. A second registration under the same name panics. The transport opens its store from a DSN, and `NewTransport` panics with `ErrOpenCache` when that fails.
- `driver.Conn` is `Get(key)`, `Set(key, value)` and `Delete(key)`. These calls take no context and no TTL.
- The cache key is the normalised request URL, so query parameters, API keys included, become part of the key.
- When a response carries `Vary`, the stored reference keeps the request values of the varied headers (`ResponseRef.VaryResolved`). `Vary: Authorization` would therefore store the bearer token.
- The cache is private: it stores `Cache-Control: private` responses and requests that carry `Authorization`.
- `stale-if-error` serves a stored response on 500, 502, 503 and 504 only (`isStaleErrorAllowed`). A transport error during revalidation is returned to the caller without falling back to the stored response (`roundtripper.go`, `handleCacheHit`).
- `stale-while-revalidate` starts one background goroutine for each stale hit. Concurrent revalidations are not collapsed, and each runs under the caller's context with `WithSWRTimeout` (default 5s).
- `net/http` cancels a request's context when the caller closes the response body if `http.Client.Timeout` is set (`setRequestCancel`), and `httpx/client` always sets it. A revalidation that inherits the caller's context therefore stops as soon as the caller has read the stale response.
- A 304 answer to a revalidation is merged into the stored entry and stored again without a second `no-store` check (`internal/validationresponsehandler.go`).
- The library reads the wall clock through its own internal clock, which callers cannot replace.

### Dragonfly as L2 server and rueidis client-side caching

rueidis enables client-side caching by default. On connect it sends `CLIENT TRACKING ON OPTIN` (rueidis v1.0.79 `pipe.go`), and `DoCache` wraps each read in `CLIENT CACHING YES`, `MULTI`, `PTTL`, the command and `EXEC`. Dragonfly supports RESP3 `CLIENT TRACKING` with `OPTIN`, `OPTOUT` and `NOLOOP` since v1.14. It does not support `BCAST`, `PREFIX` or `REDIRECT` (dragonflydb/documentation `docs/command-reference/server-management/client-tracking.md` and the compatibility page). Dragonfly's own tests cover the opt-in mode: `ClientTrackingOnAndOff`, `ToggleTrackingOnAndOff` and `ClientTrackingInfo` in `src/server/server_family_test.cc`, and "resp3 optin tracking" in `tests/dragonfly/connection_test.py`, both at commit 151c5bbc. An earlier incompatibility with rueidis, dragonflydb/dragonfly issue 2454, was fixed by dragonflydb/dragonfly PR 2724.

We ran rueidis v1.0.79 `DoCache` against `ghcr.io/dragonflydb/dragonfly:v2.1.0` with one and with four proactor threads, and against `redis:8-alpine` for comparison. All three gave the same sequence: first read a miss, second read a local hit, then a write from another connection, after which the next read was a miss with the new value. Dragonfly therefore delivered the invalidation push. `testutil/dragonfly` keeps this run as a regression test.

`cache/twotier` reads L2 through `Do`, not `DoCache`, so the cache in this ADR does not depend on client-side caching. Consequently, Dragonfly is a supported L2 server for both `cache/twotier` and rueidis client-side caching in opt-in mode. Broadcast-mode tracking (`BCAST`, `PREFIX`) is not available on Dragonfly.

## Considered options

| Option | Evaluation |
| :--- | :--- |
| a) Extend `httpx/extclient` | Rejected. Its key hashes credentials, it ignores HTTP caching headers, and it is a typed client, not a transport. Adding RFC 9111 there would leave two cache semantics in one package. |
| b) New package `httpx/httpcache`, hand-written RFC 9111 on `pquerna/cachecontrol` | Rejected. It would hand-write validation, `Vary`, stale serving and storage, which bartventer/httpcache already implements and tests. |
| c) New package `httpx/httpcache`: bartventer/httpcache over a `cache/twotier` driver, with wrappers that enforce the secret and stale rules | Chosen. |

## Decision

Option c. `httpx/httpcache` provides an `http.RoundTripper` wrapper, and `httpx/ratelimit` gains an outbound per-host limiter transport. Applications compose them as the innermost transport of `httpx/client`, with the cache outside the limiter so that hits use no provider quota:

```text
httpx/client (breaker, retry, otel)
  -> httpcache outer wrapper   strip secrets from URL; bypass auth paths; map provider errors back
    -> bartventer/httpcache    RFC 9111 / RFC 5861 over the twotier driver
      -> httpcache inner wrapper  restore secrets; singleflight; bounded in-flight; response policy
        -> ratelimit transport  per-host fixed window
          -> base transport
```

The wrappers cover the library properties listed in the context:

- **Secrets stay out of keys and entries.** The outer wrapper removes configured secret query parameters before the library computes its key, and the inner wrapper puts them back before the request leaves the process. The inner wrapper also removes secret header names from response `Vary`, so the library never records credential values in stored references. The cache key contains no secrets, so a rotated token does not empty the cache. Each transport instance therefore serves one set of provider credentials: one principal per instance.
- **Credentialed responses need a shared directive or a shared host.** Responses to credentialed requests require public, s-maxage, or must-revalidate under RFC 9111 section 3.5, and cannot vary on secret headers. A shared host explicitly asserts independence from caller credentials.
- **Credential-bearing responses are never stored.** The cache is bypassed for request paths whose segments match configured authentication segments (defaults such as `login`, `oauth`, `token`, `pin`, `pins`). The inner wrapper marks a response `no-store` when it has status 401, 403 or 407, a `Set-Cookie` or `WWW-Authenticate` header, a configured secret header, a body over the size bound, or a body matching a configured credential field such as `access_token` or `authToken`.
- **Stale on provider error.** The inner wrapper sets the configured `stale-if-error` window on storable responses. It turns transport errors into a marked 504 and provider 429 responses into a marked 503, so the library's stale-if-error path covers them. When the library has nothing stale to serve, the outer wrapper returns the original error or response.
- **Revalidation outlives the response.** The outer wrapper hands the library a context that the caller cannot cancel, and links it back to the caller's context until the response body is closed, so foreground upstream calls still stop when the caller cancels. A stale response is unlinked at once: its background revalidation runs until it completes or `revalidate_timeout` expires (`TestCacheSWRSurvivesCallerClose`).
- **Sensitive 304 responses are never merged.** When the policy marks a 304 `no-store`, a caller that sent its own validators receives that 304. For any other caller, the inner wrapper repeats the request without validators and returns the full response, which the policy also keeps out of storage (`TestCacheSensitive304NeverStored`).
- **Bounded upstream work.** The inner wrapper collapses concurrent identical upstream requests through `cache/singleflight` and caps upstream requests in flight. Background revalidation has a separate nonblocking budget capped by max_in_flight; work beyond that budget is rejected before joining a flight, while admitted work also uses the upstream cap.
- **Per-host freshness override.** A host entry with a TTL replaces the response's freshness lifetime with `max-age` and keeps validators, so the entry is revalidated with `If-None-Match` after the TTL.
- **Storage.** The library sees a `driver.Conn` backed by `cache/twotier.Typed`. One driver is registered once per process under a package-owned scheme, and each instance is looked up by an identifier in the DSN. Every storage call runs under a bounded context, because `driver.Conn` passes none.

## Consequences

- **Positive**: Applications get standards-based caching with stale fallback, shared across replicas through Redis or Dragonfly (`TestCacheSharedL2_RealRedis`, `TestCacheSharedL2_RealDragonfly`). The cache keeps no provider credentials in keys or entries.
- **Positive**: Outbound rate limiting reuses the `ulule/limiter/v3` limiter construction that inbound limiting already uses.
- **Negative**: The root module gains `github.com/bartventer/httpcache`, a pre-1.0 library with one maintainer. The wrappers depend on its stale-if-error status list and on its keying by URL. Tests in `httpx/httpcache` pin both.
- **Negative**: Freshness tests cannot use a fake clock, because the library reads the wall clock internally. Boundary tests use `Age` and `max-age` header arithmetic instead of waiting.
- **Negative**: Background revalidation is best effort. It ends after `revalidate_timeout`, and a failed revalidation leaves the stale entry in place for the next request.
- **Negative**: Secrets carried in path segments are not removed; only query parameters and headers are.
- **Follow-up**: `httpx/extclient` keeps its own in-memory GET cache. Moving it onto `httpx/httpcache` is tracked separately to keep one cache implementation (HISS-19).
- **Revisit trigger**: bartventer/httpcache accepts an injected `driver.Conn` or clock, serves stale on transport errors, or reaches v1.0; or a maintained RFC 9111 client transport is listed in awesome-go.

## References

- RFC 9111 (HTTP Caching), sections 3.5 (authenticated responses), 4.1 (`Vary`), 4.2.4 (serving stale) and 4.3 (validation).
- RFC 5861 (`stale-while-revalidate`, `stale-if-error`); RFC 8246 (`immutable`).
- bartventer/httpcache v0.14.0: `roundtripper.go`, `store/cache.go`, `store/driver/driver.go`, `internal/entry.go`, `internal/helpers.go`.
- rueidis v1.0.79: `pipe.go` (`CLIENT TRACKING ON OPTIN`), `DoCache`.
- dragonflydb/documentation: `docs/command-reference/server-management/client-tracking.md`; Redis compatibility page.
- dragonflydb/dragonfly at 151c5bbc: `src/server/server_family_test.cc`, `tests/dragonfly/connection_test.py`; issue 2454; PR 2724.
- Operator order under answer-ledger id `golusoris-outbound-http-cache-dragonfly-20261113` (2026-11-13): put the metadata cache in golusoris on the rueidis client with Dragonfly as server (paraphrased).
