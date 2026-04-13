# Agent guide — auth/session/

Server-side session management. Session ID in a cookie; data in a
pluggable Store. Ships a `MemoryStore` for tests and a `Store` interface
for production implementations (Postgres, Redis).

## Usage

```go
store := session.NewMemoryStore() // or your Postgres/Redis impl
mgr := session.NewManager(store, session.Options{
    CookieName: "sid",
    TTL:        24 * time.Hour,
    Secure:     true,
})

// In a handler:
sess, _ := mgr.Load(r)
sess.Set("user_id", "u-123")
_ = mgr.Save(w, sess)

// Log out:
_ = mgr.Destroy(w, r)
```

## Store contract

Implement `session.Store` with `Load / Save / Delete`. For Redis, store
the JSON blob under the session ID key with `SETEX`. For Postgres use a
`sessions` table with a `expires_at` column + a periodic cleanup job.

## Don't

- Don't use `MemoryStore` in production — it's not shared across replicas.
- Don't store the password or raw credential in the session.
- Don't use non-`HttpOnly` cookies for the session ID — XSS would steal it.
