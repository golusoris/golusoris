// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jcs_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/golusoris/golusoris/core/codec/jcs"
)

// FuzzCanonicalize checks, for any input: invalid JSON is never accepted,
// valid JSON is refused only for an I-JSON reason, and accepted input yields
// valid JSON that is a fixed point (canon(canon(x)) == canon(x)) and decodes
// to the same value as the input.
func FuzzCanonicalize(f *testing.F) {
	seeds, _ := filepath.Glob(filepath.Join(vectors, "input", "*.json"))
	for _, path := range seeds {
		data, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	for _, s := range []string{
		`{"a":1,"a":2}`, `"\ud800"`, `[1e400]`, `[-0, 1E2, 0.000001, 1e-7, 123456789012345678901]`,
		`{"\u00e9":1,"e":2,"\ud83d\ude00":3}`, ` [ ] `, `{"":{"":[]}}`, "\"\xff\"",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := jcs.Canonicalize(in)
		valid := json.Valid(in)
		if err != nil {
			if valid && errors.Is(err, jcs.ErrSyntax) {
				t.Fatalf("valid JSON %q refused as syntax error: %v", in, err)
			}
			return
		}
		if !valid {
			t.Fatalf("invalid JSON %q accepted as %q", in, out)
		}
		again, err := jcs.Canonicalize(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Fatalf("not a fixed point: %q -> %q -> %q (%v)", in, out, again, err)
		}
		var a, b any
		if json.Unmarshal(in, &a) != nil || json.Unmarshal(out, &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("value changed: %q -> %q", in, out)
		}
	})
}
