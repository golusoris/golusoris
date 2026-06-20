# ADR-0015: torrent client abstraction and backend dependencies

- **Status**: Accepted
- **Date**: 2026-06-20
- **Deciders**: @lusoris
- **Tags**: torrent, media, backends, dependencies

## Context

PLAN.md §4.12 calls for a `torrent/` module: a single `Client` interface (add by
magnet/URL/file, remove, list, get/status, pause, resume, basic stats) with
selectable backends for **rtorrent**, **qBittorrent**, and **transmission**,
chosen by config — mirroring how `storage/` selects local vs s3.

Three daemons, three transport protocols (rtorrent XML-RPC, qBittorrent WebAPI
v2 over HTTP, transmission JSON-RPC). Per principles.md §2 each third-party
client must be a maintained, server-appropriate library with a clean license,
justified against ecosystem alternatives. Two cross-cutting constraints shape
the choice: every backend's `*http.Client` must carry a request `Timeout` (CI
rule `http-client-must-set-timeout`), and any startup side effect (qBittorrent's
session-cookie login) must be wired via `fx.Lifecycle`, never `init()`.

## Decision

Ship one `torrent.Client` interface with a normalised `Torrent`/`State`/`Stats`
view, and three backends selected by `torrent.backend`:

- **rtorrent** → **`github.com/autobrr/go-rtorrent` v1.12.0** (MIT). The
  maintained hard-fork of the archived `mrobinsn/go-rtorrent`; used in autobrr
  production. Injects a timeout-bearing `*http.Client` via `WithHTTPClient`.
- **qBittorrent** → **`github.com/autobrr/go-qbittorrent` v1.16.0** (MIT). First-party
  autobrr client tracking WebAPI 2.13, actively maintained (commits through
  2026-05). Cookie login wired on `fx.Lifecycle` `OnStart`; the lib also
  auto-relogins on cookie expiry. Pause/resume are version-aware (it routes the
  WebAPI 2.11 `pause`→`stop` / `resume`→`start` rename for us).
- **transmission** → **`github.com/hekmon/transmissionrpc/v3` v3.0.0** (MIT). Targets RPC
  v17 / Transmission v4 (current); transparently handles the
  `X-Transmission-Session-Id` 409 CSRF handshake. Injects a timeout-bearing
  `*http.Client` via `Config.CustomClient`. transmission is the **default**
  backend (it returns the info-hash synchronously on add and needs no login).

All three are MIT and accept a custom `*http.Client`, so the framework's
mandatory request timeout is plumbed uniformly. Unknown or not-compiled backends
fail construction with `ErrUnsupportedBackend`.

## Alternatives considered

| Daemon | Option | Pros | Cons | Why not chosen |
|---|---|---|---|---|
| rtorrent | **autobrr/go-rtorrent** (chosen) | Maintained fork, MIT, ctx-aware, `WithHTTPClient` for timeout | Low commit churn (stable surface) | Best-maintained rtorrent XML-RPC client; autobrr-production-proven. |
| rtorrent | mrobinsn/go-rtorrent | Original API | Archived/read-only; README redirects to the autobrr fork | Abandoned upstream. |
| qBittorrent | **autobrr/go-qbittorrent** (chosen) | Active (2026-05), MIT, version-aware pause/stop, cookie+API-key auth, ctx variants | `Config.Timeout` is seconds not `time.Duration` (minor wart) | Most complete + best-maintained WebAPI v2 client. |
| qBittorrent | cehbz/qbittorrent | Maintained | Smaller method surface | Less coverage than autobrr's. |
| qBittorrent | l3uddz/go-qbt | — | Abandoned (2021, pre-v5 API) | Stale. |
| transmission | **hekmon/transmissionrpc/v3** (chosen) | MIT, RPC v17/v4-current, automatic CSRF handshake, `CustomClient` injection, ctx-first API | Dormant (no commits since 2023-10); protocol is stable so functional risk is low | Correct shape, complete surface; the RPC protocol's stability de-risks the dormancy. |
| transmission | GianniBYoung/transmissionrpc/v3 | API-identical fork, tagged more recently (v3.0.2, 2024-05) | Smaller user base | Documented drop-in fallback if hekmon's dormancy ever bites. |
| transmission | pborzenkov/go-transmission | Different API | v4 path untested; smaller | Re-learn an API for no gain. |

## Consequences

- **Positive**: One stable `Client` contract across three daemons; backends swap
  via a single config key with no app code change. All deps MIT with uniform
  timeout injection. The HTTP/RPC backends are unit-tested against
  real-API-shaped `httptest` servers (qBittorrent WebAPI cookie/version/CRUD,
  transmission JSON-RPC with the 409 handshake + tag echo, rtorrent XML-RPC
  per-field + `d.multicall2`); real-daemon integration is gated behind
  testcontainers `SkipIfProviderIsNotHealthy`.
- **Negative**: Backend capability is not uniform — rtorrent and qBittorrent do
  not return an info-hash synchronously on add (callers re-`List`), and rtorrent
  has no single "state" field, so `State` is approximated from completion +
  activity flags. transmission's client is dormant.
- **Follow-ups**: Pin all three in Renovate. If hekmon's transmission client
  stays dormant past a Transmission RPC bump, switch to the API-identical
  `GianniBYoung` fork. A future per-torrent status batch for rtorrent could
  replace the lazy `GetStatus` round-trips in `List`/`Get`.

## References

- PLAN.md §4.12 — `torrent/` module spec and dependency hint.
- `github.com/autobrr/go-rtorrent` v1.12.0, MIT — rtorrent XML-RPC client.
- `github.com/autobrr/go-qbittorrent` v1.16.0, MIT — qBittorrent WebAPI v2 client.
- `github.com/hekmon/transmissionrpc/v3` v3.0.0, MIT — transmission RPC client.
- `torrent/AGENTS.md` — resulting API + config surface.
- principles.md §2.5 / §2.7 — dependency, security, and supply-chain standards.
