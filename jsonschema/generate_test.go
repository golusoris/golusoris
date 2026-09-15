// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jsonschema_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/jsonschema"
)

// address is the nested struct used by generatePerson below.
type address struct {
	City string `json:"city"`
	Zip  string `json:"zip,omitempty"`
}

// generatePerson exercises nested (Address), optional (Age, Tags — both
// omitempty) and enum (Status, via the jsonschema struct tag) fields.
type generatePerson struct {
	Name    string   `json:"name"`
	Age     int      `json:"age,omitempty" jsonschema:"minimum=0"`
	Address address  `json:"address"`
	Tags    []string `json:"tags,omitempty"`
	Status  string   `json:"status" jsonschema:"enum=active,enum=inactive,enum=pending"`
}

// withChan and withComplex carry kinds invopop/jsonschema's reflector cannot
// represent and panics on; Generate must recover and report an error instead.
type withChan struct {
	C chan int `json:"c"`
}

type withComplex struct {
	Z complex128 `json:"z"`
}

// listNode is a self-referential type (a singly-linked list node): its Next
// field points back to its own type. A reflector that inlines the root
// definition (ExpandedStruct) deletes that definition from "$defs" after
// copying it to the document root, so the "next" field's "$ref" back to
// "listNode" — including the one belonging to listNode's own recursive
// case — becomes dangling. Generate must instead keep listNode itself under
// "$defs" and reference it from the root, so the document stays
// self-contained no matter how deep the chain goes.
type listNode struct {
	Value int       `json:"value"`
	Next  *listNode `json:"next,omitempty"`
}

// mutualA and mutualB are mutually recursive: mutualA embeds a pointer to
// mutualB and vice versa. Inlining whichever type is the reflection root
// deletes only that type's own "$defs" entry, leaving the other type's
// "$ref" back to it dangling — so this pair catches a class of breakage a
// single self-referential type does not: the dangling reference is not to
// the root type's own recursive field, but to the root type as referenced
// from a *different* definition.
type mutualA struct {
	Name string   `json:"name"`
	B    *mutualB `json:"b,omitempty"`
}

type mutualB struct {
	Label string   `json:"label"`
	A     *mutualA `json:"a,omitempty"`
}

// containsStr reports whether items contains an element equal to want.
func containsStr(items []any, want string) bool {
	return slices.ContainsFunc(items, func(item any) bool {
		s, ok := item.(string)
		return ok && s == want
	})
}

// resolveDef decodes doc, follows its root "$ref" into "$defs" (the
// self-contained shape Generate must produce — see TestGenerate_SelfContained
// for the general assertion), and returns the resolved definition object.
func resolveDef(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatalf("generated document is not valid JSON: %v", err)
	}
	ref, _ := decoded["$ref"].(string)
	if ref == "" {
		t.Fatalf("decoded[$ref] = %q, want a non-empty root reference into $defs", ref)
	}
	name := strings.TrimPrefix(ref, "#/$defs/")
	defs, _ := decoded["$defs"].(map[string]any)
	def, _ := defs[name].(map[string]any)
	if def == nil {
		t.Fatalf("$defs[%q] missing; root $ref %q does not resolve inside the document (defs: %v)", name, ref, defs)
	}
	return def
}

func TestGenerate_NestedOptionalEnum(t *testing.T) {
	t.Parallel()
	doc, err := jsonschema.Generate(generatePerson{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	def := resolveDef(t, doc)
	if def["type"] != "object" {
		t.Fatalf("type = %v, want %q", def["type"], "object")
	}

	required, _ := def["required"].([]any)
	for _, want := range []string{"name", "address", "status"} {
		if !containsStr(required, want) {
			t.Errorf("required = %v, want it to contain %q", required, want)
		}
	}
	for _, optional := range []string{"age", "tags"} {
		if containsStr(required, optional) {
			t.Errorf("required = %v, want it to NOT contain optional field %q", required, optional)
		}
	}

	properties, _ := def["properties"].(map[string]any)
	status, _ := properties["status"].(map[string]any)
	enum, _ := status["enum"].([]any)
	for _, want := range []string{"active", "inactive", "pending"} {
		if !containsStr(enum, want) {
			t.Errorf("status enum = %v, want it to contain %q", enum, want)
		}
	}
}

// TestGenerate_SelfContained pins the shape Generate must produce: a root
// "$ref" resolving into "$defs", never an inlined expansion of the root
// type. This is what makes recursive types representable at all — see
// TestGenerate_SelfReferential and TestGenerate_MutuallyRecursive below,
// which would fail to even compile under the previous (ExpandedStruct)
// behavior because inlining the root deletes its own "$defs" entry.
func TestGenerate_SelfContained(t *testing.T) {
	t.Parallel()
	doc, err := jsonschema.Generate(generatePerson{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatalf("generated document is not valid JSON: %v", err)
	}
	if _, ok := decoded["$ref"].(string); !ok {
		t.Fatalf("decoded[$ref] = %v (%T), want a string root reference", decoded["$ref"], decoded["$ref"])
	}
	if _, ok := decoded["$defs"].(map[string]any); !ok {
		t.Fatalf("decoded[$defs] = %v (%T), want a $defs map", decoded["$defs"], decoded["$defs"])
	}
	// The previous (broken) behavior inlined the root type's properties
	// directly onto the document, which would make this key present.
	if _, ok := decoded["properties"]; ok {
		t.Errorf("decoded[properties] present at the document root; root type must be $ref-only, not inlined")
	}
}

// TestGenerate_SelfReferential proves a self-referential type (a linked-list
// node whose own field points back to its own type) produces a schema that
// is not just valid JSON but a compilable, self-contained JSON Schema: it
// compiles via Compile and validates a real multi-node chain via Validate.
// Before this fix (ExpandedStruct: true), the reflector deleted listNode's
// own "$defs" entry after inlining it at the root, leaving the "next"
// field's "$ref": "#/$defs/listNode" dangling — Compile itself failed with
// "json-pointer ... not found", before validation was ever reached.
func TestGenerate_SelfReferential(t *testing.T) {
	t.Parallel()
	chain := listNode{Value: 1, Next: &listNode{Value: 2, Next: &listNode{Value: 3}}}

	doc, err := jsonschema.Generate(chain)
	if err != nil {
		t.Fatalf("Generate(self-referential listNode): %v", err)
	}

	def := resolveDef(t, doc)
	properties, _ := def["properties"].(map[string]any)
	next, _ := properties["next"].(map[string]any)
	if ref, _ := next["$ref"].(string); ref != "#/$defs/listNode" {
		t.Fatalf(`properties["next"]["$ref"] = %q, want "#/$defs/listNode"`, ref)
	}

	sch, err := jsonschema.Compile("list-node.json", doc)
	if err != nil {
		t.Fatalf("Compile(self-referential schema) = %v, want nil (schema must be self-contained)", err)
	}
	sample, err := json.Marshal(chain)
	if err != nil {
		t.Fatalf("marshal sample chain: %v", err)
	}
	if err := sch.Validate(sample); err != nil {
		t.Fatalf("Validate(3-node chain) against its own generated schema = %v, want nil", err)
	}

	if err := jsonschema.RoundTrip(chain); err != nil {
		t.Fatalf("RoundTrip(self-referential listNode chain) = %v, want nil", err)
	}
}

// TestGenerate_MutuallyRecursive proves a mutually-recursive pair of types
// (mutualA <-> mutualB, neither self-referential on its own) round-trips.
// Before this fix, reflecting from mutualA inlined and deleted mutualA's own
// "$defs" entry, leaving mutualB's "$ref": "#/$defs/mutualA" dangling — a
// failure mode a single self-referential type does not exercise, since there
// the dangling reference always pointed at the (also deleted) root type from
// its own field, not from a distinct definition.
func TestGenerate_MutuallyRecursive(t *testing.T) {
	t.Parallel()
	a := mutualA{Name: "ping", B: &mutualB{Label: "pong", A: &mutualA{Name: "ping-2"}}}

	doc, err := jsonschema.Generate(a)
	if err != nil {
		t.Fatalf("Generate(mutually recursive mutualA): %v", err)
	}

	var decoded map[string]any
	if err = json.Unmarshal(doc, &decoded); err != nil {
		t.Fatalf("generated document is not valid JSON: %v", err)
	}
	defs, _ := decoded["$defs"].(map[string]any)
	for _, name := range []string{"mutualA", "mutualB"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("$defs[%q] missing; want both sides of the recursive pair present (defs: %v)", name, defs)
		}
	}

	sch, err := jsonschema.Compile("mutual-a.json", doc)
	if err != nil {
		t.Fatalf("Compile(mutually recursive schema) = %v, want nil (schema must be self-contained)", err)
	}
	sample, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal sample: %v", err)
	}
	if err := sch.Validate(sample); err != nil {
		t.Fatalf("Validate(mutualA sample) against its own generated schema = %v, want nil", err)
	}

	if err := jsonschema.RoundTrip(a); err != nil {
		t.Fatalf("RoundTrip(mutually recursive mutualA) = %v, want nil", err)
	}
}

func TestGenerate_UnsupportedType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		v        any
		wantType string
	}{
		{"chan field", withChan{}, "jsonschema_test.withChan"},
		{"complex128 field", withComplex{}, "jsonschema_test.withComplex"},
		{"bare chan", make(chan int), "chan int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := jsonschema.Generate(tt.v)
			if err == nil {
				t.Fatalf("Generate(%s) = nil error, want error", tt.name)
			}

			var unsupported *jsonschema.ErrUnsupportedType
			if !errors.As(err, &unsupported) {
				t.Fatalf("Generate(%s) error = %v (%T), want an *jsonschema.ErrUnsupportedType", tt.name, err, err)
			}
			if unsupported.Type != tt.wantType {
				t.Errorf("ErrUnsupportedType.Type = %q, want %q", unsupported.Type, tt.wantType)
			}
			// The recovered raw panic value must never be forwarded
			// verbatim: the error text is ours (derived from v's Go type
			// via reflection), not the underlying library's internal
			// panic message.
			if strings.Contains(err.Error(), "invopop") {
				t.Errorf("error text %q leaks the underlying library's name", err.Error())
			}
		})
	}
}

func TestGenerate_EmptyStruct(t *testing.T) {
	t.Parallel()
	doc, err := jsonschema.Generate(struct{}{})
	if err != nil {
		t.Fatalf("Generate(struct{}{}) = %v, want nil", err)
	}

	sch, err := jsonschema.Compile("empty.json", doc)
	if err != nil {
		t.Fatalf("Compile(generated empty schema) = %v, want nil", err)
	}
	if err := sch.Validate([]byte(`{}`)); err != nil {
		t.Fatalf("Validate({}) against generated empty-struct schema = %v, want nil", err)
	}
}

func TestRoundTrip_ValidSample(t *testing.T) {
	t.Parallel()
	p := generatePerson{
		Name:    "ada",
		Address: address{City: "london"},
		Status:  "active",
	}
	if err := jsonschema.RoundTrip(p); err != nil {
		t.Fatalf("RoundTrip(valid person) = %v, want nil", err)
	}
}

func TestRoundTrip_ConstraintViolation(t *testing.T) {
	t.Parallel()
	p := generatePerson{
		Name:    "ada",
		Age:     -5, // violates jsonschema:"minimum=0"
		Address: address{City: "london"},
		Status:  "active",
	}
	if err := jsonschema.RoundTrip(p); err == nil {
		t.Fatal("RoundTrip(negative age) = nil, want error for minimum=0 violation")
	}
}

func TestRoundTrip_UnsupportedType(t *testing.T) {
	t.Parallel()
	err := jsonschema.RoundTrip(withChan{})
	if err == nil {
		t.Fatal("RoundTrip(withChan{}) = nil, want error from Generate")
	}
	var unsupported *jsonschema.ErrUnsupportedType
	if !errors.As(err, &unsupported) {
		t.Fatalf("RoundTrip(withChan{}) error = %v (%T), want it to wrap *jsonschema.ErrUnsupportedType", err, err)
	}
}

func TestRoundTrip_EmptyStruct(t *testing.T) {
	t.Parallel()
	if err := jsonschema.RoundTrip(struct{}{}); err != nil {
		t.Fatalf("RoundTrip(struct{}{}) = %v, want nil", err)
	}
}
