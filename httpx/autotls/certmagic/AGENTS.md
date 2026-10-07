<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/autotls/certmagic

caddyserver/certmagic wrapper.

## Conventions

- `http.autotls.certmagic.domains` = required; IDNA-normalized, deduplicated public names; max 100 inputs.
- `http.autotls.certmagic.timeout` = startup issuance bound; default 5m; max 30m.
- `http.autotls.certmagic.staging=true` = Let's Encrypt staging; rehearsal only; browser-invalid certificates.
- Direct lifecycle = `New(opts)` -> `Start(ctx)` -> `TLSConfig()` -> `Close()`.
- TLS ALPN = `h2`, `http/1.1`, retained `acme-tls/1` challenge protocol.
- Fx lifecycle = `Module`; startup management + cache shutdown owned by hooks.
- Distributed storage = `Options.Storage` or fx-provided `certmagic.Storage`; manager-local config; no package-global mutation.

## Don't

- Staging switch on production cache -> issuer mismatch + cache churn.
