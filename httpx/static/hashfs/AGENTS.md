<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/static/hashfs

Serves content-addressable asset filenames with year-long cache headers.

## Conventions

- Wrap asset FS once: `assetsFS := hashfs.New(embeddedAssets)`.
- In templates: `<link href="/assets/{{ $.Assets.HashName "logo.png" }}">`. rendered URL includes hash, so cache-busting is free.
- Mount: `r.Mount("/assets", hashfs.Handler(assetsFS))`. Requests for `/assets/logo-abc.png` resolve to unhashed file transparently.

## Don't

- Don't emit year-long cache on *unhashed* assets — they can't be invalidated except by renaming. Use `httpx/static` for those.
