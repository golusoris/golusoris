<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/sentry

Sentry client + slog bridge. Off by default (empty DSN = no-op).

## Conventions

- `sentry.dsn` gates enablement. No DSN skips SDK initialization and slog fanout handler installation.
- slog bridge captures:

- `slog.ErrorContext` / `slog.Error` → Sentry event (with attrs as extras)
- `slog.Warn` → Sentry breadcrumb (attached to future events)

- Lower levels are ignored — Sentry is for errors, not noise.
- Flush grace (`sentry.flush.timeout`, default 5s) runs on fx Stop so in-flight events don't get dropped.

## Tests

- `sentry_test.go`, `handler_test.go`: DSN gating, flush, defaults, slog bridge level routing and fanout. No event leaves process.
- Gap: no test sends event to Sentry-protocol server; transport, envelope encoding and DSN auth not exercised. Closing route: Bugsink (single-container Sentry-SDK-compatible server) under testcontainers.

## Don't

- Don't use Sentry for metrics/traces without Sentry Performance budget. OTel is routing layer for those.
