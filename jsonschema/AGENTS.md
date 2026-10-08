<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — jsonschema/

JSON-Schema validation (draft 2020-12 by default) over
`santhosh-tekuri/jsonschema/v6`, plus schema **generation** from Go types via
`invopop/jsonschema`. Stateless utility — **no fx wiring** (like `hash/`,
`markdown/`). Apps import it directly.

## API

```go
sch, err := jsonschema.Compile("user.json", schemaBytes) // compile once
err = sch.Validate(payloadBytes)                          // validate many (raw JSON)
err = sch.ValidateValue(decodedAny)                       // validate a json.Unmarshal'd value

doc, err := jsonschema.Generate(User{})   // reflect a Go type into a schema document
err = jsonschema.RoundTrip(User{Name: "ada"}) // Generate + Compile + Validate in one call

var unsupported *jsonschema.ErrUnsupportedType
if errors.As(err, &unsupported) { ... } // Generate/RoundTrip on a chan/func/complex/unsafe.Pointer type
```

`*Schema` is immutable + safe for concurrent use. Compile at startup, reuse handle per request.

## Config schema (Helm `values.schema.json`)

```go
type values struct {
    DB dbpgx.Options `koanf:"db"`
}
doc, err := jsonschema.GenerateConfig(values{DB: dbpgx.DefaultOptions()},
    jsonschema.ConfigOptions{EnvPrefix: "APP_", Title: "myapp"})
```

- Names from `koanf` tags; `koanf:"-"` / `jsonschema:"-"` skipped (code-only fields, func/interface types); untagged embedded struct hoisted.
- Defaults from passed value: scalars, non-empty strings, scalar slices; durations as `"5s"`. Nil pointer struct -> no defaults, env names kept.
- `time.Duration` -> `type: string` + `DurationPattern` (`TestDurationPatternMatchesParseDuration` keeps it equal to `time.ParseDuration`).
- Leaf description: `Env: APP_DB_POOL_MAX.`; underscore key -> names `CompoundKeys` entry; slice -> comma-separated; map -> `APP_X_*`.
- Nothing required; `additionalProperties: false` everywhere -> typos fail `helm lint`.
- `DoNotReference` reflector -> per-path schemas (no `$defs`); recursive type -> `*ErrUnsupportedType` via iterative cycle check (HISS-01), never reflector stack overflow.
- Golden: `__snapshots__/config_test.snap`; refresh `UPDATE_SNAPS=true go test ./jsonschema/`.

## Why santhosh-tekuri/jsonschema/v6 (validation)

- Most complete draft support (2020-12 / 2019-09 / draft-7/6/4) of Go libs;
 passes official JSON-Schema-Test-Suite.
- Zero non-stdlib deps; no CGO. Alternatives considered: `xeipuuv/gojsonschema`
 (unmaintained, draft-4 only), `qri-io/jsonschema` (draft-7, lighter coverage).

## Why invopop/jsonschema (generation)

- Verified on pkg.go.dev (2026-09): `invopop/jsonschema` latest is v0.14.0
 (April 2026, active — Go ≥1.24 requirement tracks current toolchain).
 `mcuadros/go-jsonschema-generator` — alternative named in
 `docs/FLEET_GO_DEMAND.md` — has no tagged release and its last commit is
 from March 2020 (pseudo-version only): unmaintained.
- Reflects nested structs, slices/maps, `omitempty`/`omitzero` → optional,
 and `jsonschema:"enum=a,enum=b"` struct tag → `enum`; matches shapes
 `Compile`/`Validate` already expect (draft 2020-12).
- **Why `santhosh-tekuri/jsonschema/v6` (existing validation dependency)
 cannot do this**: verified by grepping its exported top-level API (v6.0.3,
 version pinned in `go.mod`) — its only exported functions are
 `UnmarshalJSON` (parse JSON into decoder's `any` shape), `NewCompiler`
 (start `Compiler`), and `LocalizableError` (format validation error).
 Nothing reflects Go type into schema; package only ever *consumes*
 already-authored schema (`Compile` + `Validate`). Generating schema
 needs `reflect`-based walk over Go struct tags — different capability
 entirely, not omission in how we call existing dependency.

### Dependency diligence (HISS-19 / hard-rule #2)

New direct dependency: `github.com/invopop/jsonschema v0.14.0`. It adds four
transitive (`// indirect`) `go.mod` entries. PR's actual `go.mod` diff confirms
this, unlike fuller test-inclusive `go mod graph` view:

- `github.com/bahlo/generic-list-go v0.2.0`
- `github.com/buger/jsonparser v1.1.2`
- `github.com/pb33f/ordered-map/v2 v2.3.1`
- `go.yaml.in/yaml/v4 v4.0.0-rc.2`

`go.yaml.in/yaml/v2` and `.../v3` already existed before this change. Module
graph output also lists `invopop/jsonschema`'s test-only dependencies:
`stretchr/testify`, `davecgh/go-spew`, `pmezard/go-difflib`, and
`gopkg.in/yaml.v3`. Those never enter our `go.mod` or build because we import
only non-test package. Reproduce full view with
`go mod graph | grep invopop`.

  ```console
  $ go mod graph | grep invopop
  github.com/golusoris/golusoris github.com/invopop/jsonschema@v0.14.0
  github.com/invopop/jsonschema@v0.14.0 github.com/pb33f/ordered-map/v2@v2.3.1
  github.com/invopop/jsonschema@v0.14.0 github.com/stretchr/testify@v1.11.1
  github.com/invopop/jsonschema@v0.14.0 github.com/bahlo/generic-list-go@v0.2.0
  github.com/invopop/jsonschema@v0.14.0 github.com/buger/jsonparser@v1.1.2
  github.com/invopop/jsonschema@v0.14.0 github.com/davecgh/go-spew@v1.1.1
  github.com/invopop/jsonschema@v0.14.0 github.com/pmezard/go-difflib@v1.0.0
  github.com/invopop/jsonschema@v0.14.0 go.yaml.in/yaml/v4@v4.0.0-rc.2
  github.com/invopop/jsonschema@v0.14.0 gopkg.in/yaml.v3@v3.0.1
  github.com/invopop/jsonschema@v0.14.0 go@1.24
  ```

**Licences** (read from each module's own licence file in local module
cache, not assumed from module name):

| Module | Version | Licence file | SPDX id | EUPL-1.2 compatible? |
| --- | --- | --- | --- | --- |
| `invopop/jsonschema` | v0.14.0 | `COPYING` (MIT wording) | `MIT` | yes — permissive |
| `pb33f/ordered-map/v2` | v2.3.1 | `LICENSE` | `Apache-2.0` | yes — permissive |
| `bahlo/generic-list-go` | v0.2.0 | `LICENSE` (Go Authors BSD template) | `BSD-3-Clause` | yes — permissive |
| `buger/jsonparser` | v1.1.2 | `LICENSE` | `MIT` | yes — permissive |
| `go.yaml.in/yaml/v4` | v4.0.0-rc.2 | `LICENSE` (dual) | `MIT OR Apache-2.0` | yes — permissive |

 All five are permissive (MIT / Apache-2.0 / BSD-3-Clause), not copyleft.
 "EUPL-1.2 compatible" here means: using unmodified permissive dependency
 never forces relicense of our EUPL-1.2 code, and none of these licences'
 own obligations (attribution + notice preservation) conflict with EUPL-1.2.
 This differs from this repo's `LICENSE` (`Compatible Licences` appendix,
 Article 5). That clause lists copyleft licences: GPL, AGPL, OSL, EPL, CeCILL,
 MPL, LGPL, EUPL, and LiLiQ-R. EUPL-covered derivative work may use listed
 outbound licence when combined with work under one. It governs outbound
 relicensing, not inbound use of unmodified permissive dependencies. Permissive
 licences therefore need not appear. None of
 these five modules are vendored into this repository (no `vendor/` dir), so
 `reuse lint` — which covers files committed to this git tree — is
 unaffected; no new `LICENSES/*.txt` entry is needed for them.

**Maintenance status** (`gh api repos/invopop/jsonschema`, 2026-09-15 live
query): latest tag `v0.14.0` was published 2026-04-23. Repository is active,
but `pushed_at` equals `published_at`. No commits follow that release. GitHub's
`open_issues_count` reports 60 open issues and PRs together. Five months without
follow-up commit signals slow maintenance, not blocking risk alone. Named
alternative `mcuadros/go-jsonschema-generator` is worse: no tagged release and
last commit in March 2020.
 `go.yaml.in/yaml/v4` is pinned at release candidate (`v4.0.0-rc.2`);  local module cache already holds newer `rc.3`/`rc.6` snapshots pulled in by
 other work in this repo, so upstream is still iterating past RC we're
 on. It is transitive-of-transitive (via `pb33f/ordered-map/v2`, not
 `invopop/jsonschema` directly) and not on our `Generate`/`RoundTrip` call
 path (see `govulncheck` below).

**Vulnerabilities** (`govulncheck ./jsonschema/...`, govulncheck v1.8.0,
vuln.go.dev DB dated 2026-09-10):

  ```console
  $ govulncheck ./jsonschema/...
  Go: go1.27.1-X:nodwarf5
  Scanner: govulncheck@v1.8.0
  DB: https://vuln.go.dev
  DB updated: 2026-09-10 14:48:42 +0000 UTC

  No vulnerabilities found.
  ```

## Notes

- `$schema` in document selects draft; absent that, compiler default
 (2020-12) applies.
- Errors are wrapped `jsonschema: ...`; `Validate` reports first failure.
- For struct-field validation (not external schemas) use `core/validate/` (go-playground)
 instead — this package is for validating against *authored JSON Schemas*.
- `Generate` always produces self-contained document. Reflected type and every
 referenced type live under `"$defs"`, including recursive references.
 Document root is `{"$ref": "#/$defs/<TypeName>", "$defs": {...}}`, never
 reflector `ExpandedStruct` output. Inlining deletes root type's `$defs` entry
 after copying it to top level. Any `$ref` back to that type then dangles,
 including self-recursive fields and mutually recursive pairs. `Compile`
 refuses that document. See
 `TestGenerate_SelfReferential` / `TestGenerate_MutuallyRecursive` in
 `generate_test.go`.
- `Generate` recovers from reflector's panic on kinds it cannot represent
 (`chan`, `func`, `complex64/128`, `unsafe.Pointer`.) and reports it as
 `ErrUnsupportedType` (carrying `v`'s Go type name) instead — never let
 that panic, or raw recovered value, escape to caller.
- `RoundTrip(v)` is convenience for tests/smoke-checks: it generates schema
 from `v`'s type, then validates `v` itself (marshaled to JSON) against it —
 useful to catch `jsonschema:"minimum=..."`/`pattern=...` struct-tag
 constraint that sample value itself violates.
