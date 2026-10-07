<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/recovery

Backup recovery codes (one-time, for MFA loss) and password-reset tokens.

## Surface

- `recovery.New(codeStore, tokenStore, clk, secret)` → `(*Service, error)`; requires 32-byte secret minimum plus at least one store.
- `IssueCodes(ctx, userID, n)` / `VerifyCode(ctx, userID, raw)` — recovery codes.
- `IssueResetToken(ctx, userID, ttl)` / `VerifyResetToken(ctx, raw)` — reset tokens.
- `CodeStore.Consume` / `TokenStore.Consume` — atomic unused-secret claims.

## Notes

- Raw codes/tokens are returned only at issuance time; storage holds HMAC-SHA256 hashes.
- Recovery code format: 13 unpadded uppercase base32 characters.
- Constructor secrets are copied; caller mutation cannot change issued-token verification.
- Codes and reset tokens remain single-use under concurrent verification.
- Store adapters never split lookup + mark.
- Issuance requires nonempty user ID; reset-token TTLs must be positive.
- Reset tokens expire exactly at `ExpiresAt` against injected clock.
- Either `CodeStore` or `TokenStore` may be nil if corresponding flow is unused.
