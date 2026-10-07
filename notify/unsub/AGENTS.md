<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — notify/unsub/

RFC 8058 one-click unsubscribe + suppression list. Generates HMAC-signed
URLs, handles POST/GET unsubscribe clicks, and stores suppressions via pluggable `Store`.

## Usage

```go
svc, err := unsub.New(store, []byte(secret))

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

Implement `unsub.Store` (`Add / IsSuppressed / Remove`). simple
Postgres table with `(email TEXT PRIMARY KEY, created_at TIMESTAMPTZ)`
is sufficient.

Constructor: nonnil store; HMAC-SHA256 secret >= 32 bytes; secret cloned.

## Don't

- Don't use URL without checking signature in Handler — forged
 unsubscribes are real attack vector.
- Don't change secret after deployment — it invalidates all existing
 one-click links in delivered emails.
