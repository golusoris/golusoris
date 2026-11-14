// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
)

func TestParseScalars(t *testing.T) {
	t.Parallel()
	document := parseDocument([]byte("on: push\njobs:\n  build:\n    steps:\n      - run: echo ok\n"))
	if document.Error != "" {
		t.Fatal(document.Error)
	}
	tests := []struct {
		name string
		path []string
		text string
		line int
		key  bool
	}{
		{"trigger key", []string{"on"}, "on", 1, true},
		{"trigger value", []string{"on"}, "push", 1, false},
		{"step key", []string{"jobs", "build", "steps", "#0", "run"}, "run", 5, true},
		{"step value", []string{"jobs", "build", "steps", "#0", "run"}, "echo ok", 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := findScalar(t, document, tt.path, tt.key)
			if got.Text != tt.text || got.Line != tt.line || got.KeyLine != tt.line {
				t.Fatalf("scalar = %+v; want text %q, line/key line %d", got, tt.text, tt.line)
			}
		})
	}
}

func findScalar(t *testing.T, document response, path []string, key bool) scalar {
	t.Helper()
	for _, item := range document.Scalars[:min(len(document.Scalars), maxNodes)] {
		if slices.Equal(item.Path, path) && item.Key == key {
			return item
		}
	}
	t.Fatalf("missing scalar path=%v key=%t", path, key)
	return scalar{}
}

func TestExpressionReferences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, text string
		refs       []string
		calls      []string
	}{
		{"object deref", "${{ github.event_name }}", []string{"github.event_name"}, nil},
		{"array deref", "${{ contains(github.*, 'id') }}", []string{"github.*"}, []string{"contains"}},
		{"literal index", "${{ github['event_name'] }}", []string{"github.event_name"}, nil},
		{"computed index", "${{ github[inputs.key] }}", []string{"github.[computed]", "inputs.key"}, nil},
		{"implicit if", "github.ref == 'refs/heads/main'", []string{"github.ref"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			document := parseDocument([]byte("jobs:\n  build:\n    if: " + tt.text + "\n"))
			if document.Error != "" {
				t.Fatal(document.Error)
			}
			got := findScalar(t, document, []string{"jobs", "build", "if"}, false)
			if got.Error != "" || !slices.Equal(got.Refs, tt.refs) || !slices.Equal(got.Calls, tt.calls) {
				t.Fatalf("scalar = %+v; want refs %v, calls %v", got, tt.refs, tt.calls)
			}
		})
	}
}

func TestInvalidDocuments(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, text string }{
		{"duplicate key", "on: push\non: pull_request\n"},
		{"merge key", "x: &checks {if: true}\njobs: {build: {<<: *checks}}\n"},
		{"non-string key", "1: value\n"},
		{"multiple documents", "on: push\n---\non: pull_request\n"},
		{"recursive alias", "x: &loop {child: *loop}\n"},
		{"invalid YAML", "jobs: [\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseDocument([]byte(tt.text)); got.Error == "" {
				t.Fatalf("accepted invalid document: %s", tt.text)
			}
		})
	}
}

func TestDocumentDepthBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		depth int
		valid bool
	}{{128, true}, {129, false}}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.depth), func(t *testing.T) {
			t.Parallel()
			text := strings.Repeat("{a: ", tt.depth) + "value" + strings.Repeat("}", tt.depth)
			got := parseDocument([]byte(text))
			if (got.Error == "") != tt.valid {
				t.Fatalf("depth %d: error = %q; want valid %t", tt.depth, got.Error, tt.valid)
			}
		})
	}
}

func runSized(t *testing.T, size int) (bytes.Buffer, error) {
	t.Helper()
	input := "value: " + strings.Repeat("x", size-len("value: "))
	var output bytes.Buffer
	err := run(t.Context(), strings.NewReader(input), &output)
	return output, err
}

func TestRunByteBoundaryExact(t *testing.T) {
	t.Parallel()
	output, err := runSized(t, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	var document response
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	got := findScalar(t, document, []string{"value"}, false)
	if document.Error != "" || len(got.Text) != maxBytes-len("value: ") {
		t.Fatalf("exact bound: error = %q, scalar bytes = %d", document.Error, len(got.Text))
	}
}

func TestRunByteBoundaryOver(t *testing.T) {
	t.Parallel()
	output, err := runSized(t, maxBytes+1)
	if err == nil || !strings.Contains(err.Error(), "byte bound") || output.Len() != 0 {
		t.Fatalf("oversized input: error = %v, output bytes = %d", err, output.Len())
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write(_ []byte) (int, error) { return 0, w.err }

func TestRunIOErrors(t *testing.T) {
	t.Parallel()
	failure := errors.New("planted I/O failure")
	tests := []struct {
		name string
		in   io.Reader
		out  io.Writer
	}{
		{"read", iotest.ErrReader(failure), io.Discard},
		{"write", strings.NewReader("on: push\n"), failingWriter{failure}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := run(t.Context(), tt.in, tt.out); !errors.Is(err, failure) {
				t.Fatalf("error = %v; want %v", err, failure)
			}
		})
	}
}

func TestRunCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, strings.NewReader("on: push\n"), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v; want context canceled", err)
	}
}
