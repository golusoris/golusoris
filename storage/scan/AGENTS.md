<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/scan/

Malware scanning of untrusted upload bytes via ClamAV's **clamd** daemon (TCP or
unix socket), over `baruwa-enterprise/clamd`. **Security-critical** — it sits in
trust boundary for user-supplied bytes (85% coverage gate).

fx-wired (`scan.Module` provides `scan.Scanner`). It deliberately does **not**
mutate `storage.Bucket` — callers compose it: scan-before-Put, or scan-on-Get
for legacy data.

## API

```go
type Scanner interface {
    Scan(ctx, r io.Reader) (Verdict, error)  // transport fail -> ErrUnavailable; infected -> Clean=false, nil err
    ScanStrict(ctx, r io.Reader) error        // folds infected into ErrInfected (scan-before-Put guard)
    Ping(ctx) error                           // daemon reachability (health probe)
}

type Verdict struct { Clean bool; Signature string; Raw string }

// Sentinels for errors.Is — fail-open vs fail-closed is the caller's call.
var ErrInfected    // a signature fired (ScanStrict only)
var ErrUnavailable // dial/timeout/daemon failure — distinct from a verdict
var ErrTooLarge    // reader exceeds max_size, rejected before dialing
var ErrUnsupported // clamd backend not compiled on this OS (non-unix); wraps errors.ErrUnsupported

// Direct construction (apps usually use Module instead):
scan.NewClamdScanner(ClamdOptions{...}, logger, clk) (Scanner, error)
scan.NewNoopScanner(logger) Scanner   // always-clean; dev/test ONLY, shouts a WARN
```

`Scanner` is safe for concurrent use. clamd backend dials fresh connection
per command (no client-side pool in v1) — N concurrent uploads = N clamd conns;
tune clamd `MaxThreads` / `MaxConnectionQueueLength` server-side.

## Config (prefix `storage.scan`)

| Key | Default | Notes |
|---|---|---|
| `backend` | `clamd` | `clamd` \| `noop` |
| `address` | `127.0.0.1:3310` | host:port (tcp) or socket path (unix) |
| `network` | `tcp` | `tcp` \| `unix` |
| `conn_timeout` | `5s` | dial timeout |
| `cmd_timeout` | `30s` | per-command (scans are slow) |
| `conn_retries` | `2` | dial retries on timeout |
| `conn_sleep` | `200ms` | between dial retries |
| `max_size` | `25MB` | client-side cap; mirror clamd `StreamMaxLength` |
| `fail_open` | `false` | **SECURITY**: false = fail-closed (scanner down ⇒ reject). Default MUST stay false. |
| `ping_on_start` | `true` | boot-time reachability probe via fx OnStart |

`max_size`: exact rational parser; KiB/MiB/GiB, 1024-based; bare number = bytes.
finite `int64` result required. overflow, NaN, infinity, positive sub-byte reject.
empty / explicit `0` = no client-side guard; clamd still enforces `StreamMaxLength`.

## Why baruwa-enterprise/clamd

- **Structured result**: `ScanReader(ctx, io.Reader) ([]*Response, error)` with
 `Response{Filename, Signature, Status, Raw}` — surfaces actual signature
 name (`Eicar-Test-Signature`), non-negotiable for audit in trust boundary.
- **ctx-first** on every method (cancellation/deadline flows from request).
- **~zero-dep** (stdlib + INSTREAM 4-byte BE framing handled for us); MPL-2.0
 (file-level copyleft, fine as unmodified import — see ADR-0008, REUSE.toml).
- Alternatives rejected: `lyimmi/go-clamd` (bool-only verdict, no signature),
 `dutchcoders/go-clamd` (unmaintained, no ctx), hand-rolled INSTREAM client
 (parsing-bug surface in trust boundary). Full rationale in ADR-0008.

## Portability

- `baruwa-enterprise/clamd` uses unix-only syscalls (`syscall.UnixRights` /
 `Sendmsg`), so clamd backend is compiled under `//go:build unix`
 (`clamd.go`, `limit.go`, and fake-clamd unit tests). `clamd_stub.go`
 (`!unix`, i.e. Windows) keeps exported API: `NewClamdScanner` and  `backend: clamd` fx path fail closed with `ErrUnsupported` (wraps
 `errors.ErrUnsupported`). Only noop backend works there — local dev on
 Windows must opt in explicitly; `fail_open` does not mask it (it only covers
 boot ping). `go build ./...` / `go vet ./...` pass on every OS.

## Notes

- **noop is opt-in only** and logs loud WARN on construction — it can never be
 silent default; selecting it in prod is visible in logs.
- **fail-closed default**: unreachable daemon fails fx startup (bounded
 `pingOnStart`, 10s cap) unless `fail_open: true`, which downgrades to WARN.
- Verdict mapping checks every line, treats **any** `FOUND` as infected, and
  reports clean only when every non-empty result is `OK`. Nil or unexpected
  results map to `ErrUnavailable`.
- Tests: unit path drives real client against **fake clamd TCP server**
(`fakeclamd_test.go`, speaks INSTREAM + PING framing) — no Docker. Real
daemon: `integration_test.go`; `integration` tag; immutable ClamAV + Ryuk
references from `internal/testimages`; EICAR vector. Never check real malware in.
- Timing convention: `clk clock.Clock` is injected for any future retry/backoff
 logic; never `time.Now()` directly.
