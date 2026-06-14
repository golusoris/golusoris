# Agent guide — version/

Build-metadata module. Provides a typed `Info` (Version, Revision, Time, Dirty,
Go) sourced from ldflags `-X` overrides when present, else from
`runtime/debug.ReadBuildInfo` `vcs.*`. Apps `fx.Supply` it instead of
hand-rolling a per-binary version string.

## Key surface

| Symbol | Purpose |
|---|---|
| `Info` | Typed build metadata (JSON-tagged) |
| `Read()` | Assemble `Info` (ldflags → build-info fallback) |
| `Info.String()` | Compact `version+revision[-dirty]` summary |
| `Module` | Provides `Info` into the fx graph |

## Stamping a release version

```sh
go build -ldflags "-X github.com/golusoris/golusoris/version.version=1.2.3" ./cmd/app
```

Also settable: `version.revision`, `version.buildAt`. Without ldflags, `Version`
falls back to the VCS tag (or `(devel)`) and Revision/Time/Dirty come from the
embedded build info — so it works in `go run` and CI without any flags.

## Wiring

```go
fx.New(
    golusoris.Version, // provides version.Info
    fx.Invoke(func(v version.Info) { /* /healthz, server-info, log attrs */ }),
)
```

## Don't

- Don't `fmt.Sprintf` your own version string — use `Info.String()` for the
  one-liner and the typed fields for structured output (`/healthz` JSON, log
  attributes, `service.version` OTel resource).
- Don't expect ldflags vars to be set in tests or `go run` — the build-info
  fallback covers those paths.
