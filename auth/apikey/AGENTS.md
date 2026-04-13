# Agent guide — auth/apikey/

HMAC-SHA256 API key issuance and verification. Keys are never stored in
plaintext — only the HMAC digest is persisted. Raw key returned once at
creation; caller must transmit it to the client immediately.

## Usage

```go
svc := apikey.New(store, apikey.Options{
    Prefix:     "sk",
    HMACSecret: []byte(secret),
})

raw, key, err := svc.Issue(ctx, userID, []string{"read", "write"})
// Store `raw` — it's shown once and not recoverable.

key, err := svc.Verify(ctx, rawFromAuthHeader)
// key.Scopes, key.OwnerID available if ok.
```

## Store contract

Apps implement `apikey.Store` backed by Postgres or Redis. The interface
is `Save / FindByID / Revoke / ListByOwner`. A sqlc-generated Postgres
implementation is the recommended approach.

## Key format

`<prefix>_<base64url-24-random-bytes>` — e.g. `sk_X7kLmN3pQ9rSvW2yZaB`.
ID (used for DB lookup) is `<prefix>_<first-8-chars>`.

## Don't

- Don't log or return the raw key more than once.
- Don't use the same HMACSecret across environments — rotate per env.
- Don't implement scope enforcement here — `authz/` handles policy checks.
