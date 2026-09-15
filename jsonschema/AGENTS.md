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
- Pulls in `pb33f/ordered-map/v2`, `bahlo/generic-list-go`, `buger/jsonparser`
  and (indirectly) a `go.yaml.in/yaml/v4` release-candidate as transitive
  deps of its ordered-property-map internals; none of them are used by our
  Generate/RoundTrip call path. Flagged here since HISS-19/hard-rule #2 asks
  every new transitive to be weighed, not silently pulled in.

## Notes

- `$schema` in the document selects the draft; absent that, the compiler default
  (2020-12) applies.
- Errors are wrapped `jsonschema: ...`; `Validate` reports the first failure.
- For struct-field validation (not external schemas) use `validate/` (go-playground)
  instead — this package is for validating against *authored JSON Schemas*.
- `Generate` recovers from the reflector's panic on kinds it cannot represent
  (`chan`, `func`, `complex64/128`, `unsafe.Pointer`, ...) and returns an error
  instead — never let that panic escape to the caller.
- `RoundTrip(v)` is a convenience for tests/smoke-checks: it generates a schema
  from `v`'s type, then validates `v` itself (marshaled to JSON) against it —
  useful to catch a `jsonschema:"minimum=..."`/`pattern=...` struct-tag
  constraint that the sample value itself violates.
