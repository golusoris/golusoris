<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/recovery

Backup recovery codes (one-time, for MFA loss) and password-reset tokens.

## Surface

- `recovery.New(codeStore, tokenStore, clk, secret)` → `(*Service, error)`; errors when `secret` is empty.
- `IssueCodes(ctx, userID, n)` / `VerifyCode(ctx, userID, raw)` — recovery codes.
- `IssueResetToken(ctx, userID, ttl)` / `VerifyResetToken(ctx, raw)` — reset tokens.

## Notes

- Raw codes/tokens are returned only at issuance time; storage holds HMAC-SHA256 hashes.
- Codes are single-use; replays return `gerr.Unauthorized`.
- Reset tokens carry their TTL; expiry is checked against the injected clock.
- Either `CodeStore` or `TokenStore` may be nil if the corresponding flow is unused.
