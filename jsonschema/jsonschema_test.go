package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/jsonschema"
)

const userSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "name": {"type": "string"},
    "age":  {"type": "integer", "minimum": 0}
  },
  "required": ["name"],
  "additionalProperties": false
}`

func TestCompile_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		id     string
		schema string
	}{
		{"empty id", "", userSchema},
		{"malformed json", "bad.json", `{"type":`},
		{"invalid schema keyword", "bad2.json", `{"type": 12345}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := jsonschema.Compile(tt.id, []byte(tt.schema)); err == nil {
				t.Fatalf("Compile(%q) = nil error, want error", tt.name)
			}
		})
	}
}

func TestSchema_Validate(t *testing.T) {
	t.Parallel()
	sch, err := jsonschema.Compile("user.json", []byte(userSchema))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{"valid full", `{"name": "ada", "age": 36}`, false},
		{"valid name only", `{"name": "ada"}`, false},
		{"missing required name", `{"age": 36}`, true},
		{"wrong type age", `{"name": "ada", "age": "old"}`, true},
		{"negative age below minimum", `{"name": "ada", "age": -1}`, true},
		{"additional property rejected", `{"name": "ada", "x": 1}`, true},
		{"malformed document json", `{"name":`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sch.Validate([]byte(tt.doc))
			if tt.wantErr && err == nil {
				t.Fatalf("Validate(%s) = nil, want error", tt.doc)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate(%s) = %v, want nil", tt.doc, err)
			}
		})
	}
}

func TestSchema_ValidateValue(t *testing.T) {
	t.Parallel()
	sch, err := jsonschema.Compile("user.json", []byte(userSchema))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if err = sch.ValidateValue(map[string]any{"name": "grace"}); err != nil {
		t.Fatalf("ValidateValue(valid) = %v, want nil", err)
	}
	err = sch.ValidateValue(map[string]any{"age": float64(5)})
	if err == nil {
		t.Fatal("ValidateValue(missing name) = nil, want error")
	}
	if !strings.Contains(err.Error(), "jsonschema:") {
		t.Fatalf("error not wrapped with package prefix: %v", err)
	}
}
