# Agent guide — httpx/extclient

Pragmatic typed external-API client factory built on `httpx/client` (retry +
circuit-breaker + otelhttp + slog). The lightweight alternative to full
ogen-codegen from a third-party OpenAPI spec: configure a `Client` per upstream
host, then call generic JSON helpers that decode into your own caller types.

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
|---|---|
| `extclient.New(opts, ...Option)` | Build a `*Client` for one host |
| `extclient.Get[T](ctx, c, path, hdrs)` | GET → decode JSON into `T` (cacheable) |
| `extclient.Post[T] / Put[T] / Delete[T]` | Mutating JSON request → decode into `T` |
| `extclient.WithCache(*memory.Cache)` | Attach pool for GET response caching |
| `extclient.WithLogger(*slog.Logger)` | Override transport logger |
| `extclient.Module` | fx module — provides `*Registry` |
| `Registry.Client(name)` | Look up a configured client by name |
| `extclient.APIError` / `ErrStatus` | Non-2xx error (`errors.As` / `errors.Is`) |

Generic helpers are package-level functions, not methods — Go has no type
params on methods, so `Client` stays non-generic and serves every response type.

## Config

Prefix `httpx.extclient.services.<name>.*` (env `APP_HTTPX_EXTCLIENT_*`):

```
httpx.extclient.services.github.base_url        = "https://api.github.com"
httpx.extclient.services.github.bearer          = "${GH_TOKEN}"
httpx.extclient.services.github.auth_header.x-api-key = "..."   # alt to bearer
httpx.extclient.services.github.headers.accept  = "application/vnd.github+json"
httpx.extclient.services.github.timeout         = "10s"
httpx.extclient.services.github.cache_ttl       = "30s"   # 0 = no caching
httpx.extclient.services.github.retry.max       = 3
httpx.extclient.services.github.breaker.max     = 5
```

## Conventions

- One `Client` per upstream host. Set a distinctive `Name` (or rely on the host
  default) so breaker state-change logs + OTel spans identify the dependency.
- `Bearer` wins over an `Authorization` entry in `AuthHeader`. Per-request
  headers override `Headers` defaults.
- Caching keys GET responses by resolved URL, only when `CacheTTL > 0` **and** a
  `*memory.Cache` is attached (`WithCache` / `memory.Module` in fx). No pool →
  caching is silently off.
- Response bodies are bounded to 8 MiB; the body is fully drained + closed so the
  connection returns to the pool.

## Don't

- Don't cache mutating verbs — only `Get` consults the cache.
- Don't pass a relative `BaseURL`; `New` rejects anything without scheme + host.
- Don't reach for full OpenAPI codegen here — this package is the deliberately
  pragmatic path. If you truly need a generated typed client, that's a separate
  ogen pipeline.
