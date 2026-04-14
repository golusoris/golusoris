# auth/magiclink

Passwordless sign-in via single-use email links.

## Surface

- `magiclink.New(store, clk, secret, ttl)` → `*Service`.
- `Issue(ctx, email)` — returns the raw token; embed in URL, email it.
- `Verify(ctx, raw)` — returns the email + consumes the link.
- `MemoryStore` — for tests.

## Notes

- Storage holds HMAC-SHA256 hashes only.
- Single-use; replay returns `gerr.Unauthorized`.
- Default TTL 15 minutes.
