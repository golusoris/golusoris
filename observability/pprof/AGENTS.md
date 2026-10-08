<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/pprof

Stdlib `net/http/pprof` handlers, optionally basic-auth gated.

## Conventions

- Mount on dedicated admin router — do NOT mount on public router. Profile endpoints stream raw runtime state.
- Always gate with basic-auth in production. Both credentials empty = explicit no-auth mode for localhost / protected internal endpoints. One missing credential = all requests rejected.
- Comparisons are constant-time (`subtle.ConstantTimeCompare`) to resist timing attacks on basic-auth check.

## Don't

- Don't expose `/debug/pprof/profile` without auth + rate limiting — it blocks app for configured duration (default 30s) and produces CPU profile anyone can harvest.
