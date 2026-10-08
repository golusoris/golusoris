<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/tlsx/tlsxtest

Test-only certificate factory for `core/tlsx` consumers. Capability key:
`test.tls_certs`.

## Key API

| Symbol | Purpose |
| --- | --- |
| `NewCA(tb)` | self-signed ECDSA P-256 CA; `CA.PEM` = certificate |
| `(*CA).Server(tb, hosts...)` | server-auth leaf; IP literal -> IP SAN, else DNS SAN |
| `(*CA).Client(tb, name)` | client-auth leaf |
| `(*CA).WriteFiles(tb, dir, pair)` | writes `cert.pem`, `key.pem`, `ca.pem`; returns `tlsx.Files` |
| `WritePair(tb, files, pair)` · `(*CA).WriteCA(tb, path)` | overwrite in place to simulate rotation |

## Rules

- Validity fixed 2000-01-01..2100-01-01. No wall clock.
- Keys generated per test run; files mode 0600 below caller dir.
- Failures call `tb.Fatalf`.

## Don't

- Don't import from production code.
- Don't commit generated PEM files.
