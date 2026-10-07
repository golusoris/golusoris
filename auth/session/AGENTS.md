<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — auth/session/

Server-side session management. Session ID in cookie; data in pluggable Store.
Ships `MemoryStore` for tests and `Store` interface for production
implementations (Postgres, Redis).

## Usage

```go
store := session.NewMemoryStore() // or your Postgres/Redis impl
mgr, err := session.NewManager(store, session.Options{
    CookieName: "sid",
    TTL:        24 * time.Hour,
})
if err != nil { /* handle configuration error */ }

// In a handler:
sess, err := mgr.Load(r)
if err != nil {
    http.Error(w, "session", http.StatusInternalServerError)
    return
}
sess.Set("user_id", "u-123")
if err := mgr.SaveContext(r.Context(), w, sess); err != nil {
    http.Error(w, "session", http.StatusInternalServerError)
    return
}

// Log out:
if err := mgr.Destroy(w, r); err != nil {
    http.Error(w, "session", http.StatusInternalServerError)
    return
}
```

## Store contract

Implement `session.Store` with context-aware `Load / Save / Delete`.
Redis: JSON blob under session ID key with `SETEX`.
Postgres: `sessions` table, `expires_at`, periodic cleanup job.

Construction rejects nil or typed-nil stores and clocks. Zero TTL selects
24-hour default; negative TTL is invalid.
Cookies = `Secure` plus `HttpOnly` by default. Local HTTP development only ->
`AllowInsecureCookie: true`.

## Don't

- Don't use `MemoryStore` in production — it's not shared across replicas.
- Don't store password or raw credential in session.
- Don't use non-`HttpOnly` cookies for session ID — XSS would steal it.
