# Agent guide — config/

The config layer: a thin koanf v2 wrapper that loads from env + optional
YAML/JSON files. File-watch is on by default, so a mounted k8s ConfigMap update
(or `SIGHUP`) fires reload callbacks without a pod restart. Apps add structured
config by `Unmarshal`-ing a config key into their own struct.

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
`CompoundKeys []string`.

## Wiring

```go
fx.New(golusoris.Core) // Module is part of Core; provides *config.Config + Options
```

`Module` provides a `*Config` from default Options (`APP_` prefix, no files,
watch on). Override by supplying your own `Options` ahead of it via `fx.Replace`
/ `fx.Decorate`. File watchers + the SIGHUP handler start on `fx.Lifecycle`
`OnStart` and stop on `OnStop`.

## Notes

- Env mapping: `APP_DB_HOST` → `db.host`. Every underscore splits on the
  delimiter unless the leaf key is listed in `CompoundKeys` (e.g.
  `search.api_key` keeps `APP_SEARCH_API_KEY` → `search.api_key`).
- Files load first (later override earlier); env loads on top. Missing files are
  skipped silently; an unsupported extension is a hard error.
- `OnChange` callbacks run synchronously in the watcher goroutine — keep them
  quick or fan out to a worker.
