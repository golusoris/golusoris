# ADR-0003: slog (stdlib) over zap for logging

- **Status**: Accepted
- **Date**: 2026-04-13 (backfill)
- **Deciders**: @lusoris
- **Tags**: core, observability, logging

## Context

Logging is cross-cutting. Choices in the Go ecosystem:

- **stdlib `log/slog`** (Go 1.21+) — structured logging, handler interface, attribute groups.
- **[uber-go/zap](https://github.com/uber-go/zap)** — fast, mature, the default for high-throughput services for ~5 years.
- **[rs/zerolog](https://github.com/rs/zerolog)** — fastest in benchmarks, JSON-only, idiomatic chained API.

The framework targets [PLAN.md §2.5](../../.workingdir/PLAN.md) compliance (NIS2 + GDPR audit trails) — logs need to be structured, redactable, and correlatable with OTel traces.

## Decision

We will use stdlib `log/slog` as the canonical logger. The framework's `log/` package provides an `*slog.Logger` via fx (text or JSON handler depending on env), and an `otel.ModuleWithSlogBridge` fans the same calls to the OTel logger provider.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| `uber-go/zap` | Fastest in 2021 benchmarks, mature, structured | External dep; zapcore API is its own world — apps bind to it everywhere | Stdlib closes the perf gap; one fewer dep in every downstream app. |
| `rs/zerolog` | Fastest today, smallest allocs | JSON-only, chained API differs from every other logger; ecosystem fragmentation | API lock-in is worse than perf gap. |
| `sirupsen/logrus` | Most-used historically | Maintenance mode; not structured-first | Project itself recommends slog/zap/zerolog now. |

## Consequences

- **Positive**: Apps depend only on `log/slog` from stdlib — no version coupling to a third-party logger. The slog→OTel bridge means one log call goes to both stderr and the OTel collector. Easier for apps to extend (custom `slog.Handler`).
- **Negative**: slog is younger; some niche features (sampling, hot-reload of log level) require custom handlers. Allocation is slightly higher than zerolog under load (acceptable for our perf envelope per [PLAN.md §2.1](../../.workingdir/PLAN.md) rule 3 — soft).
- **Follow-ups**: Apps that need <100ns/log allocs can wrap slog with their own zerolog handler — documented escape hatch.

## References

- [slog proposal](https://go.googlesource.com/proposal/+/master/design/56345-structured-logging.md).
- See `log/AGENTS.md` for handler choice guidelines.
- [observability/sentry](../../observability/sentry/) — slog-bridge pattern that demonstrates handler composition.
