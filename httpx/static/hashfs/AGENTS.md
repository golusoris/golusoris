# Agent guide — httpx/static/hashfs

Serves content-addressable asset filenames with year-long cache headers.

## Conventions

- Wrap the asset FS once: `assetsFS := hashfs.New(embeddedAssets)`.
- In templates: `<link href="/assets/{{ $.Assets.HashName "logo.png" }}">`. The rendered URL includes the hash, so cache-busting is free.
- Mount: `r.Mount("/assets", hashfs.Handler(assetsFS))`. Requests for `/assets/logo-abc.png` resolve to the unhashed file transparently.

## Don't

- Don't emit year-long cache on *unhashed* assets — they can't be invalidated except by renaming. Use `httpx/static` for those.
