<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/magiclink

Passwordless sign-in via single-use email links.

## Surface

- `magiclink.New(store, clk, secret, ttl)` → `(*Service, error)`; requires nonnil store, 32-byte secret minimum, and nonnegative TTL.
- `Issue(ctx, email)` — returns raw token; embed in URL, email it.
- `Verify(ctx, raw)` — returns email + consumes link.
- `Store.Consume` — atomic unused-link claim. Never split lookup + mark.
- `MemoryStore` — concurrency-safe test / single-process store.
- `NewMemoryStoreWithClock`: nil and typed-nil clock -> real clock.

## Notes

- Storage holds HMAC-SHA256 hashes only.
- Constructor secrets are copied; caller mutation cannot change issued-token verification.
- Single-use under concurrent verification; replay returns `gerr.Unauthorized`.
- Default TTL 15 minutes; tokens expire exactly at `ExpiresAt`.
