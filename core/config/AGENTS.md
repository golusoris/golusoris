<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — config/

config layer: thin koanf v2 wrapper that loads from env + optional
YAML/JSON files. File-watch is on by default, so mounted k8s ConfigMap update
(or `SIGHUP`) fires reload callbacks without pod restart. Apps add structured
config by `Unmarshal`-ing config key into their own struct.

## API

```go
type Config struct{ /* ... */ }
func (c *Config) Get(path) string          // "" if absent (also String/Bool/Int/Int64/Float/Strings)
func (c *Config) Exists(path) bool
func (c *Config) All() map[string]any
func (c *Config) Unmarshal(path, into any) error // koanf tag; "5s"→time.Duration, "a,b"→[]string
func (c *Config) OnChange(fn func())             // fires on file change / SIGHUP

func New(Options) (*Config, error)
```

`Options` (zero value usable: env-only, prefix `APP_`, delimiter `.`):
`EnvPrefix`, `Delimiter`, `Files []string`, `Watch bool` (default true),
`CompoundKeys []string`, `Logger *slog.Logger` (reload-failure sink; nil =
`slog.Default()`), `SecretDirs []string`, `FileEnvSuffix string`,
`MaxSecretBytes int64` (0 = `DefaultMaxSecretBytes`, 64 KiB).

## Precedence (low -> high)

| Layer | Source | Notes |
| --- | --- | --- |
| 1 | `Files` in order | missing skipped; bad extension = error |
| 2 | `SecretDirs` in order | file name = koanf path; content `TrimSpace`d; dot entries + subdirs skipped; missing dir skipped |
| 3 | env `APP_*` | `APP_DB_HOST` -> `db.host` |
| 4 | `APP_*<FileEnvSuffix>` | file content -> target key; both `APP_X` + `APP_X_FILE` = error |

- Reload (watch / SIGHUP) re-applies layers 2-4 after file reload -> file never beats secret/env; rotated secret dir values land on SIGHUP.
- Secret dir = Kubernetes secret volume: `os.Root` follows `key -> ..data/key` symlinks, refuses escape; >1024 entries, empty key segment, oversize (`ErrSecretTooLarge`), non-regular file = error.
- Rename Secret keys to config paths via volume `items[].path` (e.g. CNPG `uri` -> `db.dsn`).
- `FileEnvSuffix` off by default (path-valued `*_FILE` keys exist); needs `EnvPrefix`. Compound key ending in suffix (`tls.cert_file`) stays plain path.

## Wiring

```go
fx.New(golusoris.Core) // Module is part of Core; provides *config.Config + Options
```

`Module` provides `*Config` from default Options (`APP_` prefix, no files,
watch on). Override by supplying your own `Options` ahead of it via `fx.Replace`
/ `fx.Decorate`. File watchers + SIGHUP handler start on `fx.Lifecycle`
`OnStart` and stop on `OnStop`.

## Notes

- Env mapping: `APP_DB_HOST` → `db.host`. Every underscore splits on  delimiter unless leaf key is listed in `CompoundKeys` (e.g.
 `search.api_key` keeps `APP_SEARCH_API_KEY` → `search.api_key`).
- Files load first (later override earlier); env loads on top. Missing files are
 skipped silently; unsupported extension is hard error.
- `OnChange` callbacks run synchronously in watcher goroutine — keep them
 quick or fan out to worker.
