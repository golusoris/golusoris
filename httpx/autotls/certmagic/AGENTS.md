<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/autotls/certmagic

caddyserver/certmagic wrapper.

## Conventions

- `http.autotls.certmagic.domains` is required.
- `http.autotls.certmagic.staging=true` points at Let's Encrypt's staging CA for rehearsals — do NOT use staging certs in production (browsers reject them).
- Storage defaults to filesystem; apps needing distributed storage configure certmagic.DefaultStorage before wiring the module.

## Don't

- Don't toggle staging back-and-forth in production — each switch invalidates cached certs for the other CA.
