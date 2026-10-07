// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jcs_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/codec/jcs"
)

func TestRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want error
	}{
		{`{"a":1,"a":2}`, jcs.ErrDuplicateKey},
		{`{"a":1,"\u0061":2}`, jcs.ErrDuplicateKey},
		{`[{"x":{"k":1,"k":1}}]`, jcs.ErrDuplicateKey},
		{`"\ud800"`, jcs.ErrLoneSurrogate},
		{`"\udc00"`, jcs.ErrLoneSurrogate},
		{`"\ud800x"`, jcs.ErrLoneSurrogate},
		{`"\ud800\u0041"`, jcs.ErrLoneSurrogate},
		{`"\ud800\ud800"`, jcs.ErrLoneSurrogate},
		{`{"\udead":1}`, jcs.ErrLoneSurrogate},
		{"\"\xff\"", jcs.ErrInvalidUTF8},
		{"\"\xc0\xaf\"", jcs.ErrInvalidUTF8},
		{"\"\xed\xa0\x80\"", jcs.ErrInvalidUTF8},
		{"{\"\xe2\x82\":1}", jcs.ErrInvalidUTF8},
		{`1e400`, jcs.ErrNumberRange},
		{`[-1e309]`, jcs.ErrNumberRange},
		{``, jcs.ErrSyntax},
		{` `, jcs.ErrSyntax},
		{`[1,]`, jcs.ErrSyntax},
		{`{"a":1,}`, jcs.ErrSyntax},
		{`{"a" 1}`, jcs.ErrSyntax},
		{`{a:1}`, jcs.ErrSyntax},
		{`{'a':1}`, jcs.ErrSyntax},
		{`[1 2]`, jcs.ErrSyntax},
		{`[`, jcs.ErrSyntax},
		{`{"a":`, jcs.ErrSyntax},
		{`]`, jcs.ErrSyntax},
		{`{} {}`, jcs.ErrSyntax},
		{`01`, jcs.ErrSyntax},
		{`1.`, jcs.ErrSyntax},
		{`.5`, jcs.ErrSyntax},
		{`+1`, jcs.ErrSyntax},
		{`1e`, jcs.ErrSyntax},
		{`-`, jcs.ErrSyntax},
		{`NaN`, jcs.ErrSyntax},
		{`Infinity`, jcs.ErrSyntax},
		{`tru`, jcs.ErrSyntax},
		{`nul`, jcs.ErrSyntax},
		{"\"tab\there\"", jcs.ErrSyntax},
		{`"\x41"`, jcs.ErrSyntax},
		{`"\u12"`, jcs.ErrSyntax},
		{`"\u12G4"`, jcs.ErrSyntax},
		{`"open`, jcs.ErrSyntax},
		{`"trailing\`, jcs.ErrSyntax},
		{"\ufeff{}", jcs.ErrSyntax},
	}
	for _, tc := range cases {
		out, err := jcs.Canonicalize([]byte(tc.in))
		if !errors.Is(err, tc.want) {
			t.Errorf("Canonicalize(%q) = %q, %v; want %v", tc.in, out, err, tc.want)
		}
	}
}

func TestRejectsNamesTheDuplicate(t *testing.T) {
	t.Parallel()
	_, err := jcs.Canonicalize([]byte(`{"b":0,"name":1,"name":2}`))
	if err == nil || !strings.Contains(err.Error(), `"name"`) {
		t.Fatalf("err = %v, want it to name the member", err)
	}
}

func TestDepthBoundary(t *testing.T) {
	t.Parallel()
	ok := strings.Repeat("[", jcs.MaxDepth) + strings.Repeat("]", jcs.MaxDepth)
	if _, err := jcs.Canonicalize([]byte(ok)); err != nil {
		t.Fatalf("MaxDepth nesting: %v", err)
	}
	deep := "[" + ok + "]"
	if _, err := jcs.Canonicalize([]byte(deep)); !errors.Is(err, jcs.ErrTooDeep) {
		t.Fatalf("MaxDepth+1 nesting: err = %v, want ErrTooDeep", err)
	}
}

func TestBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`{}`, `{}`},
		{`[]`, `[]`},
		{" \t\n\r[ { } , [ ] ]\r\n\t ", `[{},[]]`},
		{`null`, `null`},
		{`true`, `true`},
		{`"x"`, `"x"`},
		{`-0`, `0`},
		{`-0.0e-5`, `0`},
		{`1E2`, `100`},
		{`1e-400`, `0`},
		{`0.1e1`, `1`},
		{`123456789012345678901234567890`, `1.2345678901234568e+29`},
		{`"\u0000\u001f\u007f\u2028"`, "\"\\u0000\\u001f\x7f\u2028\""},
		{`"\b\f\n\r\t\/\\\""`, `"\b\f\n\r\t/\\\""`},
		{`{"":0,"a":[{"z":1,"y":2}]}`, `{"":0,"a":[{"y":2,"z":1}]}`},
		{`{"\ud83d\ude00":1,"\uffff":2}`, "{\"\U0001F600\":1,\"\uffff\":2}"},
	}
	for _, tc := range cases {
		if got := canon(t, tc.in); got != tc.want {
			t.Errorf("Canonicalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
