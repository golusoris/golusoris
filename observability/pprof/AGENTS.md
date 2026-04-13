# Agent guide — observability/pprof

Stdlib `net/http/pprof` handlers, optionally basic-auth gated.

## Conventions

- Mount on a dedicated admin router — do NOT mount on the public router. Profile endpoints stream raw runtime state.
- Always gate with basic-auth in production. Empty `User` is only acceptable for localhost / internal admin endpoints protected by network policy.
- Comparisons are constant-time (`subtle.ConstantTimeCompare`) to resist timing attacks on the basic-auth check.

## Don't

- Don't expose `/debug/pprof/profile` without auth + rate limiting — it blocks the app for the configured duration (default 30s) and produces a CPU profile anyone can harvest.
