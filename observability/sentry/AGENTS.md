# Agent guide — observability/sentry

Sentry client + slog bridge. Off by default (empty DSN = no-op).

## Conventions

- `sentry.dsn` gates enablement. No DSN means the SDK isn't initialized and the slog fanout handler isn't installed.
- slog bridge captures:
  * `slog.ErrorContext` / `slog.Error` → Sentry event (with attrs as extras)
  * `slog.Warn` → Sentry breadcrumb (attached to future events)
- Lower levels are ignored — Sentry is for errors, not noise.
- Flush grace (`sentry.flush.timeout`, default 5s) runs on fx Stop so in-flight events don't get dropped.

## Don't

- Don't use Sentry for metrics/traces unless you actually have a Sentry Performance budget. OTel (Step 5a) is the routing layer for those.
