<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/

OCI/Docker image-registry client over
[google/go-containerregistry](https://github.com/google/go-containerregistry)'s
`pkg/v1/remote`: parse a reference, resolve a tag to its content digest, fetch
a manifest, list tags, and copy an image (or index) between registries.

## API

```go
c := registry.New(registry.Options{}, nil, nil) // nil+nil = authn.DefaultKeychain + http.DefaultTransport

digest, err := c.Resolve(ctx, "gcr.io/distroless/static:nonroot")   // name.Digest, via HEAD
man,    err := c.Manifest(ctx, "gcr.io/distroless/static@"+digest.DigestStr())
tags,   err := c.ListTags(ctx, "gcr.io/distroless/static")
err          = c.Copy(ctx, "gcr.io/distroless/static:nonroot", "my-registry.example.com/static:nonroot")

ref, err := registry.ParseReference("nginx:1.27") // pure parse, no I/O
```

Every network method takes a `context.Context` **and** is additionally bounded
by `Options.Timeout` (default `registry.DefaultTimeout`, 30s) — HISS-02: a
caller that forgets a deadline still gets one.

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
```

`authn.Keychain` and `http.RoundTripper` are **optional** fx dependencies of
the module — provide your own to override the defaults:

```go
fx.Provide(func() authn.Keychain { return authn.NewMultiKeychain(ecrHelper, authn.DefaultKeychain) }),
fx.Provide(func() http.RoundTripper { return myMTLSTransport }),
```

## Why go-containerregistry

Google's `go-containerregistry` is the reference Go implementation of the OCI
distribution + image-spec client (it's what `crane`, `ko`, and `skopeo`'s Go
callers build on) — correct digest verification, registry auth resolution
matching `docker`/`crane`, and an in-process test registry
(`pkg/registry`) this package's own tests run against. No credible
alternative covers auth + manifest + copy semantics as completely.

## Notes

- **Own go.mod sub-module.** `go-containerregistry`'s dependency graph (docker
  cli config parsing, credential helpers, OCI image-spec types) is not part of
  the root module's graph; import via the full module path.
- **`Client` is stateless config, safe for concurrent use** — it builds a
  fresh `remote.Puller`/`remote.Pusher` per call rather than holding one open;
  there is no connection lifecycle to manage (`Module` needs no `OnStop`).
- **Auth**: `authn.DefaultKeychain` resolves credentials the way
  `docker`/`crane` do (`~/.docker/config.json` + registered cloud credential
  helpers). Pass an explicit `authn.Keychain` to pin credentials or run
  anonymous (tests use `authn.NewMultiKeychain()`, which always resolves to
  `authn.Anonymous`).
- **`Copy`** does one `Get` + one `Push`: `remote.Puller.Get` returns a
  `remote.Taggable` regardless of whether the source is a single-platform
  image or a multi-platform index, so `Copy` doesn't need to type-switch —
  it forwards whatever it fetched.
- **Digest validation is go-containerregistry's, not ours.** When a reference
  carries an explicit digest, `remote` hashes the response itself and errors
  on mismatch before this package ever sees the bytes — see `copy_test.go`'s
  `corruptingTransport` for how the boundary test forces that path.

## Don't

- Don't add a persistent `*http.Client`/connection pool here — `Options.Transport`
  is a `http.RoundTripper` the caller owns; this package never calls
  `CloseIdleConnections` because it never opens a client of its own.
- Don't reach for `crane` — it's a CLI-oriented convenience layer over the same
  `remote` package; wrapping `remote` directly keeps auth/transport/context
  injection explicit, which is the point of this package.
