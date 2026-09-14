// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package yaml_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/codec/yaml"
)

type manifest struct {
	Version int               `yaml:"version"`
	Name    string            `yaml:"name"`
	Tags    []string          `yaml:"tags,omitempty"`
	Meta    map[string]string `yaml:"meta,omitempty"`
}

func TestMarshalRoundTrip(t *testing.T) {
	t.Parallel()
	in := manifest{Version: 1, Name: "praetor", Tags: []string{"a", "b"}, Meta: map[string]string{"k": "v"}}
	data, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), "  - a\n") {
		t.Fatalf("expected 2-space indent, got:\n%s", data)
	}
	var out manifest
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Name != in.Name || len(out.Tags) != 2 || out.Meta["k"] != "v" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestUnmarshalStrictVsLenient(t *testing.T) {
	t.Parallel()
	src := []byte("version: 1\nname: x\nunknown_field: boom\n")
	var m manifest
	if err := yaml.Unmarshal(src, &m); err == nil {
		t.Fatal("strict decode must reject unknown fields")
	}
	if err := yaml.UnmarshalLenient(src, &m); err != nil {
		t.Fatalf("lenient decode: %v", err)
	}
	if m.Name != "x" {
		t.Fatalf("lenient decode lost data: %+v", m)
	}
}

func TestDecodeBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    yaml.Options
		input   string
		wantErr error
	}{
		{name: "empty document is a no-op", input: "", wantErr: nil},
		{name: "exactly at limit passes", opts: yaml.Options{MaxSize: 8}, input: "name: x\n", wantErr: nil},
		{name: "one byte over limit fails closed", opts: yaml.Options{MaxSize: 7}, input: "name: x\n", wantErr: yaml.ErrTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var m manifest
			err := tc.opts.Decode(strings.NewReader(tc.input), &m)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestUnmarshalInvalid(t *testing.T) {
	t.Parallel()
	var m manifest
	if err := yaml.Unmarshal([]byte("version: [unterminated"), &m); err == nil {
		t.Fatal("expected syntax error")
	}
	if err := yaml.Unmarshal(bytes.Repeat([]byte("a"), int(yaml.MaxDocumentSize)+1), &m); !errors.Is(err, yaml.ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestReadWriteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "m.yaml")
	in := manifest{Version: 2, Name: "fleet"}
	if err := yaml.WriteFile(path, in, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp file leaked: %d entries", len(entries))
	}
	var out manifest
	if err := yaml.ReadFile(path, &out); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if out.Version != in.Version || out.Name != in.Name {
		t.Fatalf("got %+v want %+v", out, in)
	}
	if err := yaml.ReadFile(filepath.Join(dir, "missing.yaml"), &out); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEncodeIndentOption(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := (yaml.Options{Indent: 4}).Encode(&buf, manifest{Tags: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "    - x\n") {
		t.Fatalf("expected 4-space indent:\n%s", buf.String())
	}
}
