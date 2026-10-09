<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/

OCI/Docker registry client over
[google/go-containerregistry](https://github.com/google/go-containerregistry)'s
`pkg/v1/remote`: parse reference, resolve tag to its content digest, fetch
manifest, list tags, copy image (or index), push/pull OCI 1.1 artifacts,
list referrers, copy image + referrers to and from OCI image-layout dirs.
One OCI client for fleet; oras-go deliberately not used.

## API

```go
c := registry.New(registry.Options{}, nil, nil) // nil+nil = authn.DefaultKeychain + private http.DefaultTransport clone

digest, err := c.Resolve(ctx, "gcr.io/distroless/static:nonroot")   // name.Digest, via HEAD
man,    err := c.Manifest(ctx, "gcr.io/distroless/static@"+digest.DigestStr())
tags,   err := c.ListTags(ctx, "gcr.io/distroless/static")
err          = c.Copy(ctx, "gcr.io/distroless/static:nonroot", "my-registry.example.com/static:nonroot")

ref, err := registry.ParseReference("nginx:1.27") // pure parse, no I/O
```

### OCI 1.1 artifacts

```go
desc, err := c.PushArtifact(ctx, "ghcr.io/org/models:v3", registry.Artifact{ // or "ghcr.io/org/models" = untagged
    ArtifactType: "application/vnd.vmafx.model.v1",
    Blobs: []registry.Blob{{MediaType: "application/vnd.vmafx.model.onnx", Path: "model.onnx"}}, // or Reader + Name
    Annotations: map[string]string{"dev.vmafx.version": "3"},
    Subject: &other,                                       // optional: referrer link
})
desc, err = c.PullArtifact(ctx, "ghcr.io/org/models@"+desc.Digest.String(), dir,
    registry.PullOptions{ArtifactType: "application/vnd.vmafx.model.v1"})
refs, err := c.Referrers(ctx, "ghcr.io/org/models:v3", "application/vnd.dev.sigstore.bundle.v0.3+json")
desc, man, err := c.ArtifactManifest(ctx, "ghcr.io/org/models@"+refs[0].Digest.String())
bundle, err := c.FetchBlob(ctx, "ghcr.io/org/models", man.Layers[0], 1<<20)
```

- Manifest: image-spec v1.1, empty config (`application/vnd.oci.empty.v1+json`), caller `artifactType` + layer media types, manifest/layer annotations, optional `subject`. No blobs -> single empty layer.
- Stable digests: nothing time-dependent added (no `created`); identical input -> identical digest. Sign by digest: `container/registry/sign` (`sign.Image`) or `cosign sign <repo>@<digest>`.
- `Blob.Name` -> `org.opencontainers.image.title` (default `filepath.Base(Path)`); pull file name = title, else `sha256-<hex>`. Name must be single local path element.
- Push target: repo (untagged) or repo:tag; digest target -> `ErrInvalidArtifact`. `Reader` blobs spooled to temp file first (digest before upload).
- Pull: manifest HEAD size check, then GET + sha256 recheck; caps (`max_blobs`, `max_blob_bytes`, `max_total_bytes`, names) checked before any write; each blob hashed while streamed into temp file in `dir`, fsync, rename only after digest match. Short/long/corrupt body -> `ErrDigestMismatch`, temp file removed.
- Referrers: ggcr `remote.Referrers` = OCI 1.1 API, fallback tag schema `sha256-<hex>`; push with `Subject` updates fallback index on registries without API (ggcr `commitSubjectReferrers`, same path cosign uses). Over `max_referrers` -> `ErrTooLarge`. Filter is client-side on descriptor `artifactType`.
- Cosign bundles: referrer `artifactType`/layer `application/vnd.dev.sigstore.bundle.v0.3+json`; `referrers_test.go` writes cosign's exact layout with raw ggcr and reads it back. In-process signing + verification = `container/registry/sign` submodule (`sign.Image`, `sign.Verify`; own go.mod, sigstore-go); this module stays sigstore-free.
- Timeouts: `PushArtifact`/`PullArtifact`/`FetchBlob` bounded by `transfer_timeout` (default 10m); `Referrers`/`ArtifactManifest` by `timeout`.
- Sentinels: `ErrTooLarge`, `ErrDigestMismatch`, `ErrArtifactType`, `ErrInvalidArtifact`.

### OCI image layouts

```go
l, err := registry.CreateLayout("/data/models/oci", registry.Options{}) // missing/empty dir -> new layout; OpenLayout = existing only
desc, err := c.CopyToLayout(ctx, "ghcr.io/org/models:v3", l)          // manifest graph + referrers -> layout
desc, err  = c.CopyFromLayout(ctx, l, desc.Digest, "registry.local/models:v3") // layout -> registry, referrers too
refs, err := l.Referrers(ctx, desc.Digest, "application/vnd.dev.sigstore.bundle.v0.3+json")
sig, err  := l.PushArtifact(ctx, registry.Artifact{ArtifactType: t, Subject: &desc}) // manifest = Client.PushArtifact one
_, man, err := l.ArtifactManifest(ctx, refs[0].Digest)
bundle, err := l.FetchBlob(ctx, man.Layers[0], 1<<20)
m, err := l.Manifest(ctx, desc.Digest) // image or index
```

- Format: OCI image-layout v1.0.0 (`oci-layout`, `index.json`, `blobs/sha256/<hex>`); ggcr `pkg/v1/layout`, oras, skopeo read + write same tree. VMAFx standalone profile + rclone mover carry such dirs.
- Referrer in layout = manifest blob + `index.json` entry (mediaType, digest, size, artifactType; no `org.opencontainers.image.ref.name`). Same convention as oras-go v2.6.2 `content/oci.Store` (lists untagged manifests). `Layout.Referrers` scans `index.json` manifests for `subject` = digest; result shaped like OCI 1.1 referrers API (artifactType, else config mediaType; manifest annotations).
- Layout = untrusted input: every manifest + blob read hashed against digest (`ErrDigestMismatch`); digest outside `sha256:<64 hex>` -> `ErrInvalidArtifact` before any path use (no `blobs/` escape); symlinked blob refused (ggcr `Path.Blob`). Manifest media type: own `mediaType` field, else `index.json` entry.
- Writes: temp file in target dir, fsync, rename only after digest match; `index.json` rewritten same way, one entry per digest; blob already present with matching hash kept. One writer process per layout; `*Layout` serializes own `index.json` updates.
- Copy (`graph.go`): post-order walk on explicit stack, no recursion (children before index: registries refuse index with missing children); referrers of every manifest in graph (index children too, `cosign sign --recursive`); referrers of referrers stay behind. Only root gets dst tag; children + referrers pushed by digest. Foreign (non-distributable) layers skipped, URL stays. Referrer artifactType read from manifest, not listing (ggcr test registry reports config mediaType).
- One verified stream (`content.go` `verifier`) for every blob: registry pull, layout read, local file push. Push/pull/copy share `sink` (`remoteSink`, `layoutSink`) + `source` (`remoteSource`, `layoutSource`); `PushArtifact` on Client + Layout = one `limits.pushArtifact`.
- Caps (copying client `Options`): manifest <= `max_manifest_bytes`; index entries + image layers <= `max_blobs` each; blob <= `max_blob_bytes`; manifests + blobs <= `max_total_bytes` per copy; referrers per manifest <= `max_referrers`; manifests per copy <= 1 + `max_blobs` + `max_referrers`. `CopyFromLayout` also applies layout read caps.
- Timeouts: copies bounded by `transfer_timeout`; `Layout.Manifest`/`ArtifactManifest`/`Referrers` by `timeout`; `Layout.PushArtifact`/`FetchBlob` by `transfer_timeout`. Local reads check ctx per chunk.
- Sign + verify inside layout: `container/registry/sign` (`sign.ImageLayout`, `sign.VerifyLayout`).
- Tests: `layout_test.go` (create/open, push + referrers, caps at + over limit, tampered flip/truncate/extend/symlink/missing, bad digests, round trip registry -> layout -> registry for tag-schema + referrers API + nested index, ggcr `validate.Index` on both ends, corrupt pull leaves no entry, foreign layer). `internal_test.go` `TestVerifier`.

Every network method takes `context.Context` **and** is additionally bounded
by `Options.Timeout` (default `registry.DefaultTimeout`, 30s) — HISS-02: caller that forgets deadline still gets one.

## fx wiring

```go
fx.New(
    config.Module,
    registry.Module,   // provides *registry.Client
    fx.Invoke(func(c *registry.Client) error {
        tags, err := c.ListTags(ctx, "gcr.io/my-proj/my-image")
        ...
    }),
)
```

Config (`container.registry.*`):

```yaml
container:
  registry:
    user_agent: my-app/1.0
    timeout: 15s
    transfer_timeout: 10m
    max_manifest_bytes: 4194304
    max_blob_bytes: 1073741824
    max_total_bytes: 4294967296
    max_blobs: 64
    max_referrers: 256
```

`authn.Keychain` and `http.RoundTripper` are **optional** fx dependencies of
module — provide your own to override defaults:

```go
fx.Provide(func() authn.Keychain { return authn.NewMultiKeychain(ecrHelper, authn.DefaultKeychain) }),
fx.Provide(func() http.RoundTripper { return myMTLSTransport }),
```

## Credentials

`container/registry/credentials` builds `authn.Keychain` chain: secret files
-> opt-in cloud workload identity (`credentials/ecr`, `credentials/gar`,
`credentials/acr`) -> `authn.DefaultKeychain`. `credentials.Module` provides
keychain; `registry.Module` picks it up as optional dependency. Details:
`credentials/AGENTS.md`.

## Why go-containerregistry

Google's `go-containerregistry` is reference Go implementation of OCI
distribution and image-spec clients. `crane`, `ko`, and `skopeo`'s Go callers
build on it. It provides digest verification, Docker-compatible registry auth,
and in-process test registry (`pkg/registry`). This package tests against that
registry. No credible alternative covers auth, manifest, and copy semantics as
completely.

## Notes

- **Own go.mod sub-module.** `go-containerregistry`'s dependency graph (docker
 cli config parsing, credential helpers, OCI image-spec types) is not part of
 root module's graph; import via full module path.
- **`Client` is stateless config, safe for concurrent use** — it builds  fresh `remote.Puller`/`remote.Pusher` per call rather than holding one open;
 there is no connection lifecycle to manage (`Module` needs no `OnStop`).
- **Auth**: `authn.DefaultKeychain` resolves credentials way
 `docker`/`crane` do (`~/.docker/config.json` + registered cloud credential
 helpers). Pass explicit `authn.Keychain` to pin credentials or run
 anonymous (tests use `authn.NewMultiKeychain()`, which always resolves to
 `authn.Anonymous`).
- **`Copy`** does one `Get` + one `Push`: `remote.Puller.Get` returns  `remote.Taggable` regardless of whether source is single-platform
 image or multi-platform index, so `Copy` doesn't need to type-switch —
 it forwards whatever it fetched.
- **Digest validation is go-containerregistry's, not ours.** For explicit
 digests, `remote` hashes response and rejects mismatches before this package
 sees bytes. `copy_test.go`'s `corruptingTransport` forces that boundary path.

## Don't

- Don't add persistent `*http.Client` here. Injected `http.RoundTripper` = caller
 owns. nil transport -> `New` clones `http.DefaultTransport` per `Client`
 (`ownTransport`, same shape as `httpx/client`; this module cannot import root),
 so no other code can close its idle pool (#703). Clone keeps 90s
 `IdleConnTimeout`; package never calls `CloseIdleConnections`.
- Don't reach for `crane` — it's CLI-oriented convenience layer over same
 `remote` package; wrapping `remote` directly keeps auth/transport/context
 injection explicit, which is point of this package.
