# Agent guide — jsonschema/

JSON-Schema validation (draft 2020-12 by default) over
`santhosh-tekuri/jsonschema/v6`. Stateless utility — **no fx wiring** (like
`hash/`, `markdown/`). Apps import it directly.

## API

```go
sch, err := jsonschema.Compile("user.json", schemaBytes) // compile once
err = sch.Validate(payloadBytes)                          // validate many (raw JSON)
err = sch.ValidateValue(decodedAny)                       // validate a json.Unmarshal'd value
```

`*Schema` is immutable + safe for concurrent use. Compile at startup, reuse the
handle per request.

## Why santhosh-tekuri/jsonschema/v6

- Most complete draft support (2020-12 / 2019-09 / draft-7/6/4) of the Go libs;
  passes the official JSON-Schema-Test-Suite.
- Zero non-stdlib deps; no CGO. Alternatives considered: `xeipuuv/gojsonschema`
  (unmaintained, draft-4 only), `qri-io/jsonschema` (draft-7, lighter coverage).

## Notes

- `$schema` in the document selects the draft; absent that, the compiler default
  (2020-12) applies.
- Errors are wrapped `jsonschema: ...`; `Validate` reports the first failure.
- For struct-field validation (not external schemas) use `validate/` (go-playground)
  instead — this package is for validating against *authored JSON Schemas*.
