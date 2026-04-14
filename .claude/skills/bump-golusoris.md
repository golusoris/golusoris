Bump golusoris in a downstream app and apply migration notes.

## Task

Bump golusoris to version: `$ARGUMENTS` (e.g. `v0.5.0` or `latest`)

## Steps

1. **Run the bump**:

```sh
golusoris bump $ARGUMENTS
# or manually:
go get github.com/golusoris/golusoris@$ARGUMENTS
go mod tidy
```

2. **Read the migration guide** for the target version:
   Check `docs/migrations/` in `github.com/golusoris/golusoris` at the target tag.
   Each file is named `v<major>.<minor>.x.md` and contains:
   - Breaking API changes with before/after snippets.
   - New required config keys.
   - Deprecated symbols (staticcheck SA1019 will surface uses).

3. **Apply codemods** — fix each breaking change listed in the migration guide:
   - Rename types/functions as described.
   - Add new required config fields with sensible defaults.
   - Replace deprecated calls.

4. **Verify**:

```sh
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
```

5. **Commit** with a conventional commit message:

```
chore(deps): bump golusoris to $ARGUMENTS

Migration: see docs/migrations/$VERSION.md
```

## Rules

- Never skip the migration guide even for patch bumps — security patches may
  change config defaults.
- If a migration requires a DB migration, run `add-migration` first.
