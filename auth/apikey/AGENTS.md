<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — auth/apikey/

HMAC-SHA256 API key issuance and verification. Keys are never stored in
plaintext — only HMAC digest is persisted. Raw key returns once at
creation; caller must transmit it immediately.
Secret change invalidates all keys. Stored digests cannot be re-hashed.

## Usage

```go
svc, err := apikey.New(store, apikey.Options{
    Prefix:     "sk",
    HMACSecret: []byte(secret),
}) // err when HMACSecret is shorter than 32 bytes

raw, key, err := svc.Issue(ctx, userID, []string{"read", "write"})
// Store `raw` — it's shown once and not recoverable.

key, err := svc.Verify(ctx, rawFromAuthHeader)
// key.Scopes, key.OwnerID available if ok.
```

## Store contract

Apps implement `apikey.Store` backed by Postgres or Redis. interface
is `Save / FindByID / Revoke / ListByOwner`. sqlc-generated Postgres
implementation is recommended approach.

## Key format

`<prefix>_<base64url-24-random-bytes>` — e.g. `sk_X7kLmN3pQ9rSvW2yZaB`.
ID (used for DB lookup) is `<prefix>_<128-bit-token-digest>`.
Verification also reads legacy `<first-segment>_<first-8-chars>` IDs so
pre-upgrade keys remain valid; new keys always use digest IDs. Both lookup
paths require the configured prefix before HMAC verification.

Constructor rejects nil stores or HMAC secrets shorter than 32 bytes, then
clones HMAC secret. Issue rejects blank
owners and clones scope metadata. Expiry is exact at `ExpiresAt`.

## Don't

- Don't log or return raw key more than once.
- Don't use same HMACSecret across environments.
- Secret rotation needs replacement keys plus coordinated cutover;
  no overlap support here.
- Don't implement scope enforcement here — `authz/` handles policy checks.
