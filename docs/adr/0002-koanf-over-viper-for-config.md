# ADR-0002: koanf over viper for configuration

- **Status**: Accepted
- **Date**: 2026-04-13 (backfill)
- **Deciders**: @lusoris
- **Tags**: core, config

## Context

Apps need layered config: defaults → file (yaml/toml/json) → env vars → flags → secrets. Two dominant Go libraries:

- **[spf13/viper](https://github.com/spf13/viper)** — the long-standing default. Heavy, monolithic, opinionated, transitive deps include 20+ packages.
- **[knadh/koanf](https://github.com/knadh/koanf)** — modular: each provider (env, file, vault, …) is a sub-package, only what you import is compiled in.

The framework's [PLAN.md §2.1](../../.workingdir/PLAN.md) requires lean dependency surface (rule 9 spirit — minimise pointer chains and transitive surface).

## Decision

We will use `github.com/knadh/koanf/v2` as the configuration loader, with the env provider as the default. The framework wraps it in a `config.Config` type that exposes `Unmarshal(prefix, &out)` so consumers don't bind to koanf directly.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| `spf13/viper` | Most popular, batteries included | Pulls 20+ transitive deps. Global state in older versions. Rigid provider model. | Dep weight + global-state legacy. |
| Hand-rolled `os.Getenv` + JSON | Zero deps | No layering, no precedence, no struct unmarshal | Apps with >10 config keys reinvent koanf badly. |
| Stdlib `flag` + env vars only | Stdlib | No file/secret providers; brittle for complex shapes | Insufficient for production apps with secrets + per-env overrides. |

## Consequences

- **Positive**: Tiny dependency footprint. Per-provider opt-in (we ship env + file out of the box; vault/etcd/consul stay opt-in). `config.Unmarshal` extended with mapstructure decode hooks for `time.Duration` + comma-separated slices.
- **Negative**: Less Stack Overflow coverage than viper. Env-var nesting limited to single underscores per koanf's parser — multi-word concepts must be grouped under sub-structs (`http.timeouts.read` not `http.timeoutsread`).
- **Follow-ups**: Document the env-var nesting convention in `config/AGENTS.md`. Add a `WithVault()` opt-in module when first app needs it.

## References

- koanf pinned at v2.3.4 — see [`docs/upstream/koanf/`](../upstream/koanf/).
- See [STATE.md decision: koanf env transform](../../.workingdir/STATE.md) — Step 3 single-underscore nesting workaround.
