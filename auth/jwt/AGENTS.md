<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — auth/jwt/

JWT sign + verify via [golang-jwt/jwt/v5]. Pure utility — no fx module.

## Usage

```go
s, err := jwt.NewHMACSigner(jwt.HS256, []byte(secret), time.Hour) // err when secret is empty

type Claims struct {
    jwt.RegisteredClaims
    UserID string `json:"uid"`
}

tok, err := s.Sign(Claims{UserID: "u-1"})
var got Claims
err = s.Parse(tok, &got)
```

## Helpers

| Function | Purpose |
|---|---|
| `NewHMACSigner(alg, secret, ttl)` | `(*Signer, error)` — HMAC signer (HS256/384/512); error on empty secret |
| `Signer.Sign(claims)` | Returns signed token string |
| `Signer.Parse(tok, &claims)` | Validates + populates claims |
| `ErrExpired(err)` | True if token is past expiry |
| `ErrInvalid(err)` | True if signature/format bad |

## Don't

- Don't store sensitive data in claims — JWTs are signed, not encrypted.
- Don't use short secrets in production — minimum 32 bytes for HS256.
- Don't use this for OIDC id_tokens — use `auth/oidc` which calls the IdP verifier.
