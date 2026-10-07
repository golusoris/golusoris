<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — secrets/

Pluggable `Secret` interface with env-var, file, and static backends.
No external dependencies — built on stdlib only.

## Backends

| Constructor | Description |
| --- | --- |
| `secrets.Env()` | Reads from `os.Getenv` |
| `secrets.File(dir)` | Root-confined regular files, trimmed, at most 64 KiB |
| `secrets.Static(map)` | Fixed map — for tests |

## Usage

```go
s := secrets.Env()
pw, err := s.Get(ctx, "DB_PASSWORD")

s2 := secrets.File("/run/secrets")
pw, err = s2.Get(ctx, "db_password")
```

## Extending

Implement `Secret` to add Vault, AWS Secrets Manager, GCP Secret Manager, etc:

```go
type vaultStore struct { client *vault.Client; mount string }
func (v vaultStore) Get(ctx context.Context, key string) (string, error) { ... }
```

Return `secrets.ErrNotFound{Key: key}` when key is absent so callers can
distinguish missing from I/O errors via `errors.As`.

`File` opens each lookup through `os.Root`; symlinks cannot escape configured
directory. All backends reject canceled contexts. `Static` clones input maps.

## Don't

- Don't log secret values — even at debug level.
- Don't store retrieved string in struct field that gets marshalled to JSON.
- Don't use `secrets.Static` in production — it embeds values in binary.
