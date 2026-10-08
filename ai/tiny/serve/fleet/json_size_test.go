// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fleet

import (
	"encoding"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
)

type allocatingJSONMarshaler struct {
	called *bool
}

func (m allocatingJSONMarshaler) MarshalJSON() ([]byte, error) {
	*m.called = true
	return make([]byte, 8<<20), nil
}

type allocatingTextMarshaler struct {
	called *bool
}

func (m allocatingTextMarshaler) MarshalText() ([]byte, error) {
	*m.called = true
	return make([]byte, 8<<20), nil
}

type allocatingJSONMarshalerTo struct {
	called *bool
}

func (m allocatingJSONMarshalerTo) MarshalJSONTo(encoder *jsontext.Encoder) error {
	*m.called = true
	return encoder.WriteValue(make([]byte, 8<<20))
}

type allocatingTextAppender struct {
	called *bool
}

func (m allocatingTextAppender) AppendText(dst []byte) ([]byte, error) {
	*m.called = true
	return append(dst, make([]byte, 8<<20)...), nil
}

var (
	_ jsonv1.Marshaler       = allocatingJSONMarshaler{}
	_ jsonv2.MarshalerTo     = allocatingJSONMarshalerTo{}
	_ encoding.TextAppender  = allocatingTextAppender{}
	_ encoding.TextMarshaler = allocatingTextMarshaler{}
)

func TestJSONStringBoundaryMatchesLegacyEncoder(t *testing.T) {
	t.Parallel()
	values := []string{
		"plain",
		"quote\"slash\\",
		"line\ncontrol\x00",
		"<script>&",
		"snowman ☃ \u2028",
		string([]byte{0xff, 0xfe}),
	}
	for _, value := range values {
		encoded, err := jsonv1.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if jsonStringExceeds(value, int64(len(encoded))) {
			t.Fatalf("%q exceeds exact encoded size %d", value, len(encoded))
		}
		if !jsonStringExceeds(value, int64(len(encoded)-1)) {
			t.Fatalf("%q fits below encoded size %d", value, len(encoded))
		}
	}
}

func TestValidateJSONInputMemoryRejectsLargeScalarAndCycle(t *testing.T) {
	t.Parallel()
	if err := validateJSONInputMemory(strings.Repeat("x", 64), 32); err == nil {
		t.Fatal("large scalar passed memory preflight")
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if err := validateJSONInputMemory(cycle, DefaultMaxInputBytes); err == nil {
		t.Fatal("cyclic input passed depth preflight")
	}
}

func TestValidateJSONInputMemoryBoundsValueCount(t *testing.T) {
	t.Parallel()
	values := make([]bool, maxInputJSONValues+1)
	if err := validateJSONInputMemory(values, DefaultMaxInputBytes); err == nil {
		t.Fatal("oversized JSON value graph passed preflight")
	}
}

func TestCheckInputSizeRejectsAllocatingCustomMarshalersBeforeCallingThem(t *testing.T) {
	t.Parallel()
	for name, build := range map[string]func(*bool) any{
		"JSON":        func(called *bool) any { return allocatingJSONMarshaler{called: called} },
		"JSON to":     func(called *bool) any { return allocatingJSONMarshalerTo{called: called} },
		"text":        func(called *bool) any { return allocatingTextMarshaler{called: called} },
		"text append": func(called *bool) any { return allocatingTextAppender{called: called} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := false
			input := build(&called)
			if err := checkInputSize(input, DefaultMaxInputBytes); err == nil {
				t.Fatal("custom marshaler passed bounded input preflight")
			}
			if called {
				t.Fatal("custom marshaler ran before rejection")
			}
		})
	}
}

func TestCheckInputSizeAcceptsBoundedRawMessage(t *testing.T) {
	t.Parallel()
	input := jsonv1.RawMessage(`{"bounded":true}`)
	if err := checkInputSize(input, len(input)); err != nil {
		t.Fatalf("checkInputSize: %v", err)
	}
}
