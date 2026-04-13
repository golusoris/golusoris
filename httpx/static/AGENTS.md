# Agent guide — httpx/static

Serves unhashed static assets with short-cache + ETag-validated 304s.

## Conventions

- Mount at a prefix: `r.Mount("/assets", static.Handler(embeddedFS, static.Options{}))`.
- Index fallback: `/` and directory paths resolve to `index.html` by default. Disable with `NoIndexFallback: true` (useful for SPAs that want a 404 from the static layer and fall-through to an API handler).
- Defaults: `Cache-Control: public, max-age=300, must-revalidate`. For hashed assets use `httpx/static/hashfs` instead so browsers cache for a year.

## Don't

- Don't serve user-uploaded content through this handler — use `storage/` (Step 13) which sets Content-Disposition + validates MIME on write. `static` assumes the FS you hand it is trusted.
