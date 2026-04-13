# Agent guide — secrets/

Pluggable `Secret` interface with env-var, file, and static backends.
No external dependencies — built on stdlib only.

## Backends

| Constructor | Description |
|---|---|
| `secrets.Env()` | Reads from `os.Getenv` |
| `secrets.File(dir)` | Each file name is the key; content (trimmed) is the value |
| `secrets.Static(map)` | Fixed map — for tests |

## Usage

```go
s := secrets.Env()
pw, err := s.Get(ctx, "DB_PASSWORD")

s2 := secrets.File("/run/secrets")
pw, err = s2.Get(ctx, "db_password")
```

## Extending

Implement `Secret` to add Vault, AWS Secrets Manager, GCP Secret Manager, etc.:

```go
type vaultStore struct { client *vault.Client; mount string }
func (v vaultStore) Get(ctx context.Context, key string) (string, error) { ... }
```

Return `secrets.ErrNotFound{Key: key}` when the key is absent so callers can
distinguish missing from I/O errors via `errors.As`.

## Don't

- Don't log secret values — even at debug level.
- Don't store the retrieved string in a struct field that gets marshalled to JSON.
- Don't use `secrets.Static` in production — it embeds values in the binary.
