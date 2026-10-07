// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jcs_test

import (
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/codec/jcs"
)

// vectors holds cyberphone/json-canonicalization testdata @ 19d51d7
// (Apache-2.0), the reference corpus RFC 8785 Appendix I points to.
const vectors = "testdata/cyberphone"

func canon(t *testing.T, in string) string {
	t.Helper()
	out, err := jcs.Canonicalize([]byte(in))
	if err != nil {
		t.Fatalf("Canonicalize(%q): %v", in, err)
	}
	return string(out)
}

func TestCyberphoneVectors(t *testing.T) {
	t.Parallel()
	inputs, err := filepath.Glob(filepath.Join(vectors, "input", "*.json"))
	if err != nil || len(inputs) != 6 {
		t.Fatalf("want 6 vector files, got %d (%v)", len(inputs), err)
	}
	for _, in := range inputs {
		data, rerr := os.ReadFile(in)
		if rerr != nil {
			t.Fatal(rerr)
		}
		want, rerr := os.ReadFile(filepath.Join(vectors, "output", filepath.Base(in)))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if got := canon(t, string(data)); got != string(want) {
			t.Errorf("%s:\n got %s\nwant %s", filepath.Base(in), got, want)
		}
	}
}

// TestRFC8785AppendixB is the number serialisation table of RFC 8785
// Appendix B; NaN and Infinity have no JSON spelling and are covered by
// TestRejects as out-of-range literals.
func TestRFC8785AppendixB(t *testing.T) {
	t.Parallel()
	table := map[string]string{
		"0000000000000000": "0",
		"8000000000000000": "0",
		"0000000000000001": "5e-324",
		"8000000000000001": "-5e-324",
		"7fefffffffffffff": "1.7976931348623157e+308",
		"ffefffffffffffff": "-1.7976931348623157e+308",
		"4340000000000000": "9007199254740992",
		"c340000000000000": "-9007199254740992",
		"4430000000000000": "295147905179352830000",
		"44b52d02c7e14af5": "9.999999999999997e+22",
		"44b52d02c7e14af6": "1e+23",
		"44b52d02c7e14af7": "1.0000000000000001e+23",
		"444b1ae4d6e2ef4e": "999999999999999700000",
		"444b1ae4d6e2ef4f": "999999999999999900000",
		"444b1ae4d6e2ef50": "1e+21",
		"3eb0c6f7a0b5ed8c": "9.999999999999997e-7",
		"3eb0c6f7a0b5ed8d": "0.000001",
		"41b3de4355555553": "333333333.3333332",
		"41b3de4355555554": "333333333.33333325",
		"41b3de4355555555": "333333333.3333333",
		"41b3de4355555556": "333333333.3333334",
		"41b3de4355555557": "333333333.33333343",
		"becbf647612f3696": "-0.0000033333333333333333",
		"43143ff3c1cb0959": "1424953923781206.2",
	}
	for bits, want := range table {
		u, err := strconv.ParseUint(bits, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		if got := canonNumber(t, math.Float64frombits(u)); got != want {
			t.Errorf("%s: got %s, want %s", bits, got, want)
		}
	}
}

// canonNumber feeds f as 17 significant digits, a spelling that round-trips
// exactly but never matches the canonical one, so parsing is exercised too.
func canonNumber(t *testing.T, f float64) string {
	t.Helper()
	return canon(t, strconv.FormatFloat(f, 'e', 16, 64))
}

// TestRFC8785Section323 is the property-sorting sample of section 3.2.3.
func TestRFC8785Section323(t *testing.T) {
	t.Parallel()
	got := canon(t, `{
		"\u20ac": "Euro Sign", "\r": "Carriage Return", "\ufb33": "Hebrew Letter Dalet With Dagesh",
		"1": "One", "\ud83d\ude00": "Emoji: Grinning Face", "\u0080": "Control",
		"\u00f6": "Latin Small Letter O With Diaeresis"}`)
	order := []string{
		"Carriage Return", "One", "Control", "Latin Small Letter O With Diaeresis",
		"Euro Sign", "Emoji: Grinning Face", "Hebrew Letter Dalet With Dagesh",
	}
	last := -1
	for _, v := range order {
		i := strings.Index(got, `"`+v+`"`)
		if i <= last {
			t.Fatalf("%q out of order in %s", v, got)
		}
		last = i
	}
}

// TestRFC8785Section324 is the UTF-8 byte sample of section 3.2.4, copied
// verbatim from the RFC.
func TestRFC8785Section324(t *testing.T) {
	t.Parallel()
	in := `{"numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
		"string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/", "literals": [null, true, false]}`
	const rfc = `
     7b 22 6c 69 74 65 72 61 6c 73 22 3a 5b 6e 75 6c 6c 2c 74 72
     75 65 2c 66 61 6c 73 65 5d 2c 22 6e 75 6d 62 65 72 73 22 3a
     5b 33 33 33 33 33 33 33 33 33 2e 33 33 33 33 33 33 33 2c 31
     65 2b 33 30 2c 34 2e 35 2c 30 2e 30 30 32 2c 31 65 2d 32 37
     5d 2c 22 73 74 72 69 6e 67 22 3a 22 e2 82 ac 24 5c 75 30 30
     30 66 5c 6e 41 27 42 5c 22 5c 5c 5c 5c 5c 22 2f 22 7d`
	want, err := hex.DecodeString(strings.Join(strings.Fields(rfc), ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := canon(t, in); got != string(want) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
