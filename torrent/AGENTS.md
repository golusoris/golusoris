# Agent guide — torrent/

Backend-agnostic `Client` over a running torrent daemon. The concrete backend
(rtorrent, qBittorrent, transmission) is selected by config, mirroring how
`storage/` selects local vs s3.

## Client interface

```go
type Client interface {
    Add(ctx, magnetOrURL string, AddOptions) (hash string, err error)
    AddFile(ctx, data []byte, AddOptions) (hash string, err error)
    Remove(ctx, hash string, deleteData bool) error
    List(ctx) ([]Torrent, error)
    Get(ctx, hash string) (Torrent, error)   // ErrNotFound when unknown
    Pause(ctx, hash string) error
    Resume(ctx, hash string) error
    Stats(ctx) (Stats, error)
}
```

`Add`/`AddFile` return the info-hash when the backend reports one
synchronously. rtorrent and qBittorrent do **not** return a hash on add, so the
returned string is empty there — re-`List` to find the new torrent. transmission
returns the hash directly.

State is normalised onto a small shared vocabulary (`downloading`, `seeding`,
`paused`, `checking`, `queued`, `error`, `unknown`); each backend's native
states map onto it.

## Backends

| Backend | Library | Wire | Notes |
|---|---|---|---|
| `transmission` (default) | hekmon/transmissionrpc/v3 | JSON-RPC | 409 CSRF handshake handled by the lib; returns hash on add |
| `qbittorrent` | autobrr/go-qbittorrent | WebAPI v2 | SID cookie login via fx `OnStart`; auto-relogin; pause/resume version-aware (2.11 stop/start rename) |
| `rtorrent` | autobrr/go-rtorrent | XML-RPC | Mutations resolve the torrent by hash first; `Get` not-found = empty name |

Unknown / not-compiled backends fail construction with `ErrUnsupportedBackend`.

## Config

Keys live under the `torrent` prefix. `timeout` caps every daemon round-trip
(applied to each backend's `*http.Client`; default 30s).

```yaml
torrent:
  backend: transmission        # or qbittorrent | rtorrent (default: transmission)
  timeout: 30s

  transmission:
    url: http://user:pass@localhost:9091/transmission/rpc

  qbittorrent:
    host: http://localhost:8080
    username: admin
    password: "..."

  rtorrent:
    addr: http://localhost:8000/RPC2
    basic_user: ""             # optional HTTP Basic
    basic_pass: ""
```

Each backend also takes `insecure_skip_verify: true` to disable TLS cert
verification — **test/dev only**, off by default.

## Wiring

```go
fx.New(
    golusoris.Core,
    torrent.Module, // provides torrent.Client
)
```

The qBittorrent backend logs in on `fx.Lifecycle` `OnStart` (bounded by fx's
start timeout) — selecting `qbittorrent` makes app start depend on the daemon
being reachable. rtorrent and transmission are stateless at construction.

## Don't

- Don't expect `Add`/`AddFile` to return a hash on rtorrent/qBittorrent — only
  transmission does. Re-`List` to find the torrent.
- Don't construct a backend's `*http.Client` without a `Timeout` — `Options.Timeout`
  is plumbed into every backend (CI rule `http-client-must-set-timeout`).
- Don't enable `insecure_skip_verify` in production — it disables TLS validation.
- Don't add per-torrent N+1 status calls in hot paths — rtorrent's `Get` already
  issues several XML-RPC round-trips; prefer `List` for bulk reads.
