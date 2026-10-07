<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/static

Serves unhashed static assets with short-cache + ETag-validated 304s.

## Conventions

- Mount at prefix: `r.Mount("/assets", static.Handler(embeddedFS, static.Options{}))`.
- Index fallback: `/` and directory paths resolve to `index.html` by default. Disable with `NoIndexFallback: true` (useful for SPAs that want 404 from static layer and fall-through to API handler).
- Defaults: `Cache-Control: public, max-age=300, must-revalidate`. For hashed assets use `httpx/static/hashfs` instead so browsers cache for year.
- Handler reads and hashes file on each request. It does not retain file data.
- `http.ServeContent` owns ETag list, wildcard, weak comparison, and method-specific preconditions.

## Don't

- Don't serve user-uploaded content through this handler. Use `storage/`, which sets Content-Disposition + validates MIME on write. `static` assumes FS you hand it is trusted.
