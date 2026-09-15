// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jsonschema_test

import (
	"encoding/json"
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

func containsStr(items []any, want string) bool {
	for _, item := range items {
		if s, ok := item.(string); ok && s == want {
			return true
		}
	}
	return false
}

func TestGenerate_NestedOptionalEnum(t *testing.T) {
	t.Parallel()
	doc, err := jsonschema.Generate(generatePerson{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatalf("generated document is not valid JSON: %v", err)
	}
	if decoded["type"] != "object" {
		t.Fatalf("type = %v, want %q", decoded["type"], "object")
	}

	required, _ := decoded["required"].([]any)
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

	properties, _ := decoded["properties"].(map[string]any)
	status, _ := properties["status"].(map[string]any)
	enum, _ := status["enum"].([]any)
	for _, want := range []string{"active", "inactive", "pending"} {
		if !containsStr(enum, want) {
			t.Errorf("status enum = %v, want it to contain %q", enum, want)
		}
	}
}

func TestGenerate_UnsupportedType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		v    any
	}{
		{"chan field", withChan{}},
		{"complex128 field", withComplex{}},
		{"bare chan", make(chan int)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := jsonschema.Generate(tt.v); err == nil {
				t.Fatalf("Generate(%s) = nil error, want error", tt.name)
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
	if err := jsonschema.RoundTrip(withChan{}); err == nil {
		t.Fatal("RoundTrip(withChan{}) = nil, want error from Generate")
	}
}

func TestRoundTrip_EmptyStruct(t *testing.T) {
	t.Parallel()
	if err := jsonschema.RoundTrip(struct{}{}); err != nil {
		t.Fatalf("RoundTrip(struct{}{}) = %v, want nil", err)
	}
}
