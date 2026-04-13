# Agent guide — testutil/snapshot/

Snapshot testing backed by gkampitakis/go-snaps.

Snapshots are stored in `__snapshots__/` next to the test file and committed
to the repository. On the first run (or after deletion) the snapshot is
created automatically.

## Usage

```go
func TestRender(t *testing.T) {
    got := render(input)
    snapshot.Match(t, got) // creates/compares __snapshots__/TestRender.snap
}

func TestRenderJSON(t *testing.T) {
    type resp struct { ID int; Name string }
    snapshot.MatchJSON(t, resp{ID: 1, Name: "Alice"})
}
```

## Updating snapshots

```sh
UPDATE_SNAPS=true go test ./...
```

## Don't

- Don't snapshot non-deterministic output (timestamps, UUIDs, random values).
  Seed randomness with `testutil/factory.New(t)` first.
- Don't delete `__snapshots__/` — it's the source of truth for the test.
  Update it with `UPDATE_SNAPS=true` when intentional changes occur.
