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

`*Schema` is immutable + safe for concurrent use. Compile at startup, reuse the
handle per request.

## Why santhosh-tekuri/jsonschema/v6 (validation)

- Most complete draft support (2020-12 / 2019-09 / draft-7/6/4) of the Go libs;
  passes the official JSON-Schema-Test-Suite.
- Zero non-stdlib deps; no CGO. Alternatives considered: `xeipuuv/gojsonschema`
  (unmaintained, draft-4 only), `qri-io/jsonschema` (draft-7, lighter coverage).

## Why invopop/jsonschema (generation)

- Verified on pkg.go.dev (2026-09): `invopop/jsonschema` latest is v0.14.0
  (April 2026, active — Go ≥1.24 requirement tracks current toolchain).
  `mcuadros/go-jsonschema-generator` — the alternative named in
  `docs/FLEET_GO_DEMAND.md` — has no tagged release and its last commit is
  from March 2020 (pseudo-version only): unmaintained.
- Reflects nested structs, slices/maps, `omitempty`/`omitzero` → optional,
  and a `jsonschema:"enum=a,enum=b"` struct tag → `enum`; matches the shapes
  `Compile`/`Validate` already expect (draft 2020-12).
- **Why `santhosh-tekuri/jsonschema/v6` (the existing validation dependency)
  cannot do this**: verified by grepping its exported top-level API (v6.0.3,
  the version pinned in `go.mod`) — its only exported functions are
  `UnmarshalJSON` (parse JSON into the decoder's `any` shape), `NewCompiler`
  (start a `Compiler`), and `LocalizableError` (format a validation error).
  Nothing reflects a Go type into a schema; the package only ever *consumes*
  an already-authored schema (`Compile` + `Validate`). Generating a schema
  needs a `reflect`-based walk over Go struct tags — a different capability
  entirely, not an omission in how we call the existing dependency.

### Dependency diligence (HISS-19 / hard-rule #2)

New direct dependency: `github.com/invopop/jsonschema v0.14.0`. It pulls in
exactly four new transitive (`// indirect`) entries in `go.mod` — confirmed
against the PR's actual `go.mod` diff, not assumed from `go mod graph`'s
fuller (test-inclusive) view: `github.com/bahlo/generic-list-go v0.2.0`,
`github.com/buger/jsonparser v1.1.2`, `github.com/pb33f/ordered-map/v2
v2.3.1`, and `go.yaml.in/yaml/v4 v4.0.0-rc.2`. (`go.yaml.in/yaml/v2` and
`.../v3`, which also appear in `go mod graph | grep invopop` below, already
existed in `go.mod` before this change — module graph pruning means the
`go mod graph` view additionally lists `invopop/jsonschema`'s own *test-only*
deps — `stretchr/testify`, `davecgh/go-spew`, `pmezard/go-difflib`,
`gopkg.in/yaml.v3` — which are never pulled into our `go.mod`/build because
we only import the library's non-test package.)

  ```
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

**Licences** (read from each module's own licence file in the local module
cache, not assumed from the module name):

| Module | Version | Licence file | SPDX id | EUPL-1.2 compatible? |
| --- | --- | --- | --- | --- |
| `invopop/jsonschema` | v0.14.0 | `COPYING` (MIT wording) | `MIT` | yes — permissive |
| `pb33f/ordered-map/v2` | v2.3.1 | `LICENSE` | `Apache-2.0` | yes — permissive |
| `bahlo/generic-list-go` | v0.2.0 | `LICENSE` (Go Authors BSD template) | `BSD-3-Clause` | yes — permissive |
| `buger/jsonparser` | v1.1.2 | `LICENSE` | `MIT` | yes — permissive |
| `go.yaml.in/yaml/v4` | v4.0.0-rc.2 | `LICENSE` (dual) | `MIT OR Apache-2.0` | yes — permissive |

  All five are permissive (MIT / Apache-2.0 / BSD-3-Clause), not copyleft.
  "EUPL-1.2 compatible" here means: using an unmodified permissive dependency
  never forces a relicense of our EUPL-1.2 code, and none of these licences'
  own obligations (attribution + notice preservation) conflict with EUPL-1.2.
  That is a distinct question from this repo's `LICENSE`
  (`Compatible Licences` appendix, Article 5): that clause lists **copyleft**
  licences (GPL, AGPL, OSL, EPL, CeCILL, MPL, LGPL, EUPL, LiLiQ-R) an
  EUPL-covered *derivative work* may be **relicensed under outbound** when
  combined with a work under one of them — it governs outbound relicensing of
  a combined work, not inbound use of an unmodified permissive dependency, so
  a permissive licence correctly does not (and need not) appear in it. None of
  these five modules are vendored into this repository (no `vendor/` dir), so
  `reuse lint` — which covers files actually committed to this git tree — is
  unaffected; no new `LICENSES/*.txt` entry is needed for them.

**Maintenance status** (`gh api repos/invopop/jsonschema`, 2026-09-15 — a
live query, not a remembered figure): latest tag `v0.14.0`, published
2026-04-23; repository not archived; `pushed_at` equals the v0.14.0
`published_at` timestamp, i.e. no commits since that release; 60 open
issues (GitHub's `open_issues_count`, which bundles open issues and open
PRs). Roughly five months without a follow-up release/commit as of this PR
is a maintenance-pace signal worth tracking, not a blocker on its own — the
named alternative (`mcuadros/go-jsonschema-generator`) is unambiguously worse
(no tagged release at all, last commit March 2020).
  `go.yaml.in/yaml/v4` is pinned at a release candidate (`v4.0.0-rc.2`); the
  local module cache already holds newer `rc.3`/`rc.6` snapshots pulled in by
  other work in this repo, so upstream is still iterating past the RC we're
  on. It is transitive-of-transitive (via `pb33f/ordered-map/v2`, not
  `invopop/jsonschema` directly) and not on our `Generate`/`RoundTrip` call
  path (see `govulncheck` below).

**Vulnerabilities** (`govulncheck ./jsonschema/...`, govulncheck v1.8.0,
vuln.go.dev DB dated 2026-09-10):

  ```
  $ govulncheck ./jsonschema/...
  Go: go1.27.1-X:nodwarf5
  Scanner: govulncheck@v1.8.0
  DB: https://vuln.go.dev
  DB updated: 2026-09-10 14:48:42 +0000 UTC

  No vulnerabilities found.
  ```

## Notes

- `$schema` in the document selects the draft; absent that, the compiler default
  (2020-12) applies.
- Errors are wrapped `jsonschema: ...`; `Validate` reports the first failure.
- For struct-field validation (not external schemas) use `validate/` (go-playground)
  instead — this package is for validating against *authored JSON Schemas*.
- `Generate` always produces a self-contained document: the reflected type
  (and every type it references, including itself) lives under `"$defs"`,
  and the document root is `{"$ref": "#/$defs/<TypeName>", "$defs": {...}}`
  — never an inlined expansion of the root type (the reflector's
  `ExpandedStruct` option). Inlining deletes the root type's own `$defs`
  entry after copying it to the top level, which breaks any `$ref` back to
  that type name — including a self-referential type's own recursive field,
  or a second type in a mutually-recursive pair — into a dangling reference
  that `Compile` then refuses to compile at all. See
  `TestGenerate_SelfReferential` / `TestGenerate_MutuallyRecursive` in
  `generate_test.go`.
- `Generate` recovers from the reflector's panic on kinds it cannot represent
  (`chan`, `func`, `complex64/128`, `unsafe.Pointer`, ...) and reports it as
  an `ErrUnsupportedType` (carrying `v`'s Go type name) instead — never let
  that panic, or the raw recovered value, escape to the caller.
- `RoundTrip(v)` is a convenience for tests/smoke-checks: it generates a schema
  from `v`'s type, then validates `v` itself (marshaled to JSON) against it —
  useful to catch a `jsonschema:"minimum=..."`/`pattern=...` struct-tag
  constraint that the sample value itself violates.
