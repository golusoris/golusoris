<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/tlsx

File-backed TLS with lazy rotation. Stdlib plus `core/clock`. No goroutine, no
fsnotify. Capability keys: `crypto.tls_files`, `crypto.tls_reload`.

## Key API

| Symbol | Purpose |
|---|---|
| `NewReloader(Files{Cert,Key,CA}, Options)` | load once; fail closed on missing file, bad PEM, key mismatch, empty CA |
| `(*Reloader).ServerConfig(clientAuth)` | TLS 1.3 server config; `GetConfigForClient` serves current cert + client CA pool |
| `(*Reloader).ClientConfig(serverName)` | TLS 1.3 client config; client cert follows files per handshake; `RootCAs` = pool at call time |
| `(*Reloader).LastError()` | last reload failure; nil after good or unchanged read |
| `ParseClientAuth(mode, haveCA)` | `none`, `request`, `require_any`, `verify_if_given`, `require_and_verify`; empty = verify when CA set |

## Behaviour

- Handshake reads files at most once per `Options.MinInterval` (default 30s, `clock.Clock` driven).
- Change detection compares bytes, not mtime. Same-size rewrite still rotates.
- Failed reload keeps last good material, logs WARN, sets `LastError`. Next interval retries.
- Verifying client-auth mode without CA fails handshake (`ErrNoClientCA`); empty `ClientCAs` would mean system roots.
- Server config clones base per handshake: set `NextProtos` etc. on returned config before first handshake.
- CA rotation on client side needs fresh `ClientConfig` per connection. `grpc` client creds do that; plain `*tls.Config` holders keep pool snapshot.

## Tests

`tlsxtest/` issues in-memory ECDSA CA + leaves, writes PEM below `t.TempDir()`. Never commit key material (gitleaks gate).

## Don't

- Don't add fsnotify or polling goroutine. Lazy check is contract.
- Don't map `require` alias. `RequireAnyClientCert` skips verification; mode names stay explicit.
- Don't lower `MinVersion` below TLS 1.3 here; callers own any downgrade.
