# auth/linking

Maps external (provider, subject) IdP identities to local user IDs.

## Surface

- `linking.New(store)` → `*Service`.
- `Link(ctx, userID, provider, subject, email)` — idempotent, conflict-on-rebind.
- `Lookup(ctx, provider, subject)` — returns the local UserID.
- `List(ctx, userID)` — all linked identities for a user.
- `Unlink(ctx, provider, subject)`.
- `MemoryStore` for tests.

## Notes

- A given `(provider, subject)` belongs to exactly one local user; rebinding to a different user returns `gerr.Conflict`.
- App stores back the `Identity` records in Postgres typically via sqlc.
