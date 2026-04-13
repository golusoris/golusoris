# Agent guide — testutil/river

Boots a real river client backed by a Postgres testcontainer for
integration tests.

## Conventions

- `Start(t, Options{Register: ...})` returns a Harness with Pool +
  Workers + Client. Tears down on t.Cleanup.
- Register workers via `opts.Register` — the client starts only when
  workers are registered (insert-only otherwise).
- `Harness.WaitForJob(ctx, kind)` polls until a job reaches a terminal
  state. Use for deterministic integration tests (no sleep-based
  waits).

## Don't

- Don't re-run the same harness across tests — fresh Postgres per test
  is the isolation contract. If that's too slow for your suite, share
  a single pool + truncate the river tables between tests.
