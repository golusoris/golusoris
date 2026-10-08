<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# httpx/extclient

JSON client per fixed upstream. Built on `httpx/client`. Bounded body. Optional
GET cache.

Redirects stay on the configured scheme and host. Cross-origin redirects fail
before bearer, custom authentication, or default headers can leave that origin.

## Usage

```go
// Direct construction (one upstream):
c, err := extclient.New(extclient.ServiceOptions{
    BaseURL: "https://api.github.com",
    Bearer:  os.Getenv("GH_TOKEN"),
    Headers: map[string]string{"Accept": "application/vnd.github+json"},
    Timeout: 10 * time.Second,
    Retry:   client.RetryOptions{Max: 3},
})

type Repo struct { FullName string `json:"full_name"` }
repo, err := extclient.Get[Repo](ctx, c, "/repos/golusoris/golusoris", nil)

// Via fx (named services from config + optional shared cache):
fx.New(
    golusoris.Core,
    memory.Module,     // optional — enables GET response caching
    extclient.Module,  // provides *extclient.Registry
    fx.Invoke(func(r *extclient.Registry) error {
        gh, err := r.Client("github")
        ...
    }),
)
```

## Key API

| Symbol | Purpose |
| --- | --- |
| `extclient.New(opts, ...Option)` | Build a `*Client` for one host |
| `extclient.Get[T](ctx, c, path, hdrs)` | Decode cacheable GET JSON |
| `extclient.Post[T] / Put[T] / Delete[T]` | Send mutation; decode JSON |
| `extclient.WithCache(*memory.Cache)` | Attach pool for GET response caching |
| `extclient.WithLogger(*slog.Logger)` | Override transport logger |
| `extclient.Module` | fx module — provides `*Registry` |
| `Registry.Client(name)` | Look up a configured client by name |
| `extclient.APIError` / `ErrStatus` | Inspect non-2xx errors |

Generic helpers are package-level functions, not methods — Go has no type
params on methods, so `Client` stays non-generic and serves every response type.

## Config

Prefix `httpx.extclient.services.<name>.*` (env `APP_HTTPX_EXTCLIENT_*`):

```toml
httpx.extclient.services.github.base_url        = "https://api.github.com"
httpx.extclient.services.github.bearer          = "${GH_TOKEN}"
httpx.extclient.services.github.auth_header.x-api-key = "..."   # alt to bearer
httpx.extclient.services.github.headers.accept  = "application/vnd.github+json"
httpx.extclient.services.github.timeout         = "10s"
httpx.extclient.services.github.cache_ttl       = "30s"   # 0 = no caching
httpx.extclient.services.github.retry.max       = 3
httpx.extclient.services.github.breaker.max     = 5
```

## Rules

- One `Client` per upstream origin. Unique `Name`.
- Helper path must be relative. Absolute URL, network path, userinfo, and
  cross-origin resolution rejected before credentials attach.
- `Bearer` wins over `Authorization` entry in `AuthHeader`. Per-request
 header wins over default header.
- Construction clones `Headers` and `AuthHeader`; caller mutation stays local.
- GET cache key = resolved URL + SHA-256 of effective headers. caller headers,
 credentials, and representations stay isolated. caching needs positive
 `CacheTTL` plus attached `*memory.Cache`; no pool means off.
- Response body cap: 8 MiB. exact limit accepted; successful overflow returns
 `ErrResponseTooLarge`. Body closes; overflow connection not reused.
- Unsafe retry follows `httpx/client`: idempotency key or explicit opt-in.

## Don't

- Don't cache mutating verbs — only `Get` consults cache.
- Don't pass relative `BaseURL`; scheme plus host required. Userinfo forbidden.
- Don't accept a caller-controlled absolute URL as helper path.
- Don't create bare `*http.Client` or use `http.DefaultClient`. outbound HTTP
 flows through `extclient`: timeout, retry, breaker, OTel.
- Don't reach for full OpenAPI codegen here — this package is deliberately
 pragmatic path. If you truly need generated typed client, that's separate
 ogen pipeline.
