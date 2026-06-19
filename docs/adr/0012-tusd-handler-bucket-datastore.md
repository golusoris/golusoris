# ADR-0012: storage/tus dependency choice

- **Status**: Accepted
- **Date**: 2026-06-19
- **Deciders**: @lusoris
- **Tags**: storage, uploads, tus

## Context

The `storage/tus` module needs a dependency. Per principles.md §2 the choice must favour a maintained, server-appropriate library with a lean footprint, justified against ecosystem alternatives.

## Decision

Use **`github.com/tus/tusd/v2/pkg/handler`** (v2.10.0). tusd v2 is the maintained reference tus 1.0 server in Go (v2.10.0, 2026-06-16; v2.9.x Feb/Mar 2026). The catalog hint does not mean the tusd binary: we import only github.com/tus/tusd/v2/pkg/handler, the embeddable protocol engine, and back it with a thin DataStore adapter over our existing storage.Bucket. Fit for a server framework: Logger is *slog.Logger natively (matches log/), NewUnroutedHandler exposes per-method handlers that map 1:1 onto chi (no StripPrefix, app middleware preserved), and it is storage-agnostic so we avoid tusd's filestore/s3store/gcsstore (which would duplicate storage/ and drag in their own SDKs). Footprint is small (stdlib + x/* + tusd internals); MIT, same as our tree. Hand-writing the full tus protocol + extensions is hundreds of lines of security-sensitive parsing best owned by an audited reference impl.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Hand-rolled tus handler | — | Large security-critical HTTP surface (offset races, version negotiation, base64 metadata, multiple extensions); high int | not chosen |
| tusd full server pkg/filestore+s3store / cmd | — | Heavy and redundant: ships its own FS/S3/GCS/Azure stores + lockers that duplicate our storage.Bucket and pull extra clo | not chosen |
| eventials/go-tus | — | Client-only and abandoned; no server http.Handler. | not chosen |
| bdragon300/tusgo | — | Modern but client-only; useful as a test client, not as the server dependency. | not chosen |
| vimeo/tusd fork | — | Downstream fork, less current than upstream tus/tusd v2; no reason to prefer over the canonical actively-released upstre | not chosen |

## Consequences

See `storage/tus/AGENTS.md` for the resulting API + config surface. The dependency is pinned and tracked by Renovate; revisit if it goes unmaintained or a better-fit library appears.
