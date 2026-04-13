# Agent guide — notify/unsub/

RFC 8058 one-click unsubscribe + suppression list. Generates HMAC-signed
URLs, handles POST/GET unsubscribe clicks, and stores suppressions via a
pluggable `Store`.

## Usage

```go
svc := unsub.New(store, []byte(secret))

// When building an email:
url := svc.URL("https://app.example.com/unsub", "user@example.com")
// Set: List-Unsubscribe: <url>
// Set: List-Unsubscribe-Post: List-Unsubscribe=One-Click

// Mount the handler:
mux.Handle("/unsub", svc.Handler())

// Before sending:
if sup, _ := svc.IsSuppressed(ctx, email); sup { return }
```

## Store contract

Implement `unsub.Store` (`Add / IsSuppressed / Remove`). A simple
Postgres table with `(email TEXT PRIMARY KEY, created_at TIMESTAMPTZ)`
is sufficient.

## Don't

- Don't use the URL without checking the signature in Handler — forged
  unsubscribes are a real attack vector.
- Don't change the secret after deployment — it invalidates all existing
  one-click links in delivered emails.
