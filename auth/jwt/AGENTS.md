<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — auth/jwt/

HMAC JWT sign + verify via [golang-jwt/jwt/v5]. Pure utility. HS256/384/512 only.
One secret. Secret change invalidates outstanding tokens. No overlap rotation.

## Usage

```go
s, err := jwt.NewHMACSigner(
    jwt.HS256,
    []byte(secret),
    time.Hour,
) // err when secret is short or TTL non-positive

type Claims struct {
    jwt.RegisteredClaims
    UserID string `json:"uid"`
}

tok, err := s.Sign(Claims{UserID: "u-1"})
var got Claims
err = s.Parse(tok, &got)
```

## Helpers

- `NewHMACSigner(alg, secret, ttl)`: TTL at least one second; key size at least digest size; real clock.
- `NewHMACSignerWithClock(alg, secret, ttl, clock)`: injected test clock.
- Constructors clone the secret and box injected clocks; `Signer` remains comparable for every clock implementation.
- `Signer.Sign(claims)`: reject nil; sign; add configured expiry when absent.
- `Signer.Parse(tok, &claims)`: reject nil claims; verify HMAC method, signature, expiry.
- `ErrExpired(err)`: expiry classification.
- `ErrInvalid(err)`: signature, format, missing-expiry, or not-yet-valid classification.

## Don't

- Don't store sensitive data in claims — JWTs are signed, not encrypted.
- Don't use short secrets — minimum 32/48/64 bytes for HS256/384/512.
- Don't promise key rotation. One signer verifies one secret.
- Don't use this for OIDC id_tokens — use `auth/oidc` which calls IdP verifier.
