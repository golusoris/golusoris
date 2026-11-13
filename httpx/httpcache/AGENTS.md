<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/httpcache/

- ADR-0028 binding; bartventer/httpcache v0.14.0 RFC engine; twotier storage.
- Composition: client -> cache -> outbound ratelimit -> base; one principal per instance.
- Config: `http.outbound.cache.*`; `DefaultOptions()` before constructor overrides.
- Host zero durations: inherit; host names exact, lowercase, no port.
- Secret/path lists: extend defaults; never replace protections.
- Driver: one registration; random instance DSN; Close deregisters; bounded store contexts.
- Upstream: singleflight GET/HEAD; bounded semaphore; oversized leader streams until Close.
- Synthetic 503/504: stale engine input only; outer restores original failure.
- Caller ctx: engine gets detached ctx, linked until body Close; STALE unlinks at once; SWR bound = revalidate_timeout.
- 304 + no-store: never merged; caller validators -> 304 passthrough; else refetch without validators.
- Store retention: freshness plus stale windows minimum; zero unlimited.
- Unit store: twotier L1-only; freshness tests Age arithmetic, no sleeps.
- Real L2: `TestCacheSharedL2_Real{Redis,Dragonfly}`; containers; `-short` skips.
