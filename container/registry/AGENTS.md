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
Delete artifact + referrer tree; collect layout garbage.
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

Every network method takes `context.Context` **and** is additionally bounded
by `Options.Timeout` (default `registry.DefaultTimeout`, 30s) — HISS-02: caller that forgets deadline still gets one.

## Don't

- Don't add persistent `*http.Client` here. Injected `http.RoundTripper` = caller
 owns. nil transport -> `New` clones `http.DefaultTransport` per `Client`
 (`ownTransport`, same shape as `httpx/client`; this module cannot import root),
 so no other code can close its idle pool (#703). Clone keeps 90s
 `IdleConnTimeout`; package never calls `CloseIdleConnections`.
- Don't reach for `crane` — it's CLI-oriented convenience layer over same
 `remote` package; wrapping `remote` directly keeps auth/transport/context
 injection explicit, which is point of this package.

Full API, wire format and notes: [REFERENCE.md](REFERENCE.md). Read it before changing this package.
