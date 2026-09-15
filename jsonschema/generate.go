// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jsonschema

import (
	"encoding/json"
	"fmt"

	invopop "github.com/invopop/jsonschema"
)

// Generate builds a JSON Schema (draft 2020-12) document describing the type
// of v, via reflection over v's Go type using invopop/jsonschema. Struct
// fields are required unless their `json` tag carries omitempty or omitzero;
// a `jsonschema:"enum=a,enum=b"` struct tag adds an enum constraint; nested
// structs are expanded into "$defs" and referenced with "$ref".
//
// Kinds the reflector cannot represent (chan, func, complex64/128,
// unsafe.Pointer, ...) make the underlying library panic; Generate recovers
// from that and reports it as an error instead of crashing the caller.
func Generate(v any) (doc []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("jsonschema: generate: %v", r)
		}
	}()
	reflector := &invopop.Reflector{ExpandedStruct: true}
	doc, err = json.Marshal(reflector.Reflect(v))
	if err != nil {
		return nil, fmt.Errorf("jsonschema: marshal generated schema: %w", err)
	}
	return doc, nil
}

// RoundTrip generates a schema for the type of v (see [Generate]), compiles
// it, and validates v — marshaled to JSON — against that schema. It is a
// smoke check that a generated schema actually accepts a value of the type
// it was derived from; a failure here means either the reflector produced an
// inconsistent schema, or a `jsonschema:"..."` struct-tag constraint (e.g.
// minimum, pattern) is violated by v itself.
func RoundTrip(v any) error {
	doc, err := Generate(v)
	if err != nil {
		return err
	}
	sch, err := Compile("generated.json", doc)
	if err != nil {
		return fmt.Errorf("jsonschema: compile generated schema: %w", err)
	}
	sample, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("jsonschema: marshal sample: %w", err)
	}
	return sch.Validate(sample)
}
