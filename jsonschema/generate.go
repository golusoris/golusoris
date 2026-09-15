// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jsonschema

import (
	"encoding/json"
	"fmt"
	"reflect"

	invopop "github.com/invopop/jsonschema"
)

// ErrUnsupportedType is returned by [Generate] (and, transitively,
// [RoundTrip]) when v's Go type — or a type reachable from it — has a kind
// the underlying reflector cannot represent as JSON Schema (chan, func,
// complex64/128, unsafe.Pointer, ...).
//
// The underlying library reports this condition by panicking rather than
// returning an error; Generate recovers from that panic and reports it via
// this typed error instead of propagating the raw recovered value, which
// could be an arbitrary runtime.Error carrying internal implementation
// details that should never leak across the package boundary.
type ErrUnsupportedType struct {
	// Type is the reflect.Type.String() representation of the Go type
	// passed to Generate.
	Type string
}

// Error implements the error interface.
func (e *ErrUnsupportedType) Error() string {
	return "jsonschema: unsupported type " + e.Type
}

// Generate builds a self-contained JSON Schema (draft 2020-12) document
// describing the type of v, via reflection over v's Go type using
// invopop/jsonschema. Struct fields are required unless their `json` tag
// carries omitempty or omitzero; a `jsonschema:"enum=a,enum=b"` struct tag
// adds an enum constraint.
//
// The reflector's default, $defs-based mode is used: v's own type, plus
// every nested or referenced type, gets an entry under "$defs", and the
// document's root is a "$ref" into that map — never an inlined expansion of
// the root type. This is required for correctness on self-referential types
// (a linked-list or tree node embedding a pointer to its own type) and on
// mutually recursive type pairs: inlining the root definition (the
// reflector's ExpandedStruct option) deletes it from "$defs" after copying
// it to the top level, so any "$ref" back to that type name — including one
// from the root type's own fields, or from another type in the recursion —
// becomes a dangling reference and the document is no longer self-contained.
// See the package tests (TestGenerate_SelfReferential,
// TestGenerate_MutuallyRecursive) for schemas that round-trip through
// [Compile] and [Schema.Validate] under this scheme, and would fail to even
// compile under ExpandedStruct.
//
// Kinds the reflector cannot represent (chan, func, complex64/128,
// unsafe.Pointer, ...) make the underlying library panic; Generate recovers
// from that and reports it as an [*ErrUnsupportedType] instead of crashing
// the caller or leaking the raw panic value.
func Generate(v any) (doc []byte, err error) {
	defer func() {
		if recover() != nil {
			doc, err = nil, &ErrUnsupportedType{Type: typeNameOf(v)}
		}
	}()
	reflector := &invopop.Reflector{}
	doc, err = json.Marshal(reflector.Reflect(v))
	if err != nil {
		return nil, fmt.Errorf("jsonschema: marshal generated schema: %w", err)
	}
	return doc, nil
}

// typeNameOf returns the Go type name of v for use in [ErrUnsupportedType],
// without panicking when v is a nil interface (reflect.TypeOf(nil) is nil,
// and calling String() on a nil reflect.Type panics).
func typeNameOf(v any) string {
	t := reflect.TypeOf(v)
	if t == nil {
		return "<nil>"
	}
	return t.String()
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
