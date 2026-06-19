// Package jsonschema validates JSON documents against JSON Schema (draft
// 2020-12 by default) via santhosh-tekuri/jsonschema/v6.
//
// It is a stateless utility — no fx wiring. Compile a schema once, validate
// many payloads:
//
//	sch, err := jsonschema.Compile("user.json", schemaBytes)
//	if err != nil { /* schema is malformed */ }
//	if err := sch.Validate(payloadBytes); err != nil {
//	    // payload violates the schema
//	}
//
// The compiled [Schema] is safe for concurrent use by multiple goroutines.
package jsonschema

import (
	"bytes"
	"errors"
	"fmt"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// Schema is a compiled JSON Schema ready to validate documents. It is immutable
// and safe for concurrent use.
type Schema struct {
	sch *v6.Schema
}

// Compile parses and compiles the JSON Schema in schemaJSON. id identifies the
// schema for error messages and $ref resolution (e.g. "user.json"); it must be
// non-empty. The schema's own $schema keyword selects the draft; absent that,
// the compiler default (2020-12) applies.
func Compile(id string, schemaJSON []byte) (*Schema, error) {
	if id == "" {
		return nil, errors.New("jsonschema: empty schema id")
	}
	doc, err := v6.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("jsonschema: parse schema %q: %w", id, err)
	}
	c := v6.NewCompiler()
	if err = c.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("jsonschema: add schema %q: %w", id, err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("jsonschema: compile schema %q: %w", id, err)
	}
	return &Schema{sch: sch}, nil
}

// Validate checks raw JSON document bytes against the schema. It returns a
// wrapped error describing the first failure, or nil if the document conforms.
func (s *Schema) Validate(documentJSON []byte) error {
	doc, err := v6.UnmarshalJSON(bytes.NewReader(documentJSON))
	if err != nil {
		return fmt.Errorf("jsonschema: parse document: %w", err)
	}
	return s.ValidateValue(doc)
}

// ValidateValue checks an already-decoded JSON value — the shape produced by
// json.Unmarshal into an any (map[string]any, []any, float64, string, bool,
// nil) — against the schema.
func (s *Schema) ValidateValue(doc any) error {
	if err := s.sch.Validate(doc); err != nil {
		return fmt.Errorf("jsonschema: validation failed: %w", err)
	}
	return nil
}
