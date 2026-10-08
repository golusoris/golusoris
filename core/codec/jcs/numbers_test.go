// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jcs_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/codec/jcs"
)

// es6Sequence reproduces the deterministic float64 sequence behind
// cyberphone's es6testfile100m.txt (testdata/README.md there): the static
// edge cases, 2000 serial subnormal neighbours, then SHA-256-chained
// pseudo-random doubles, skipping zero, NaN and the infinities.
type es6Sequence struct {
	static []uint64
	idx    int
	block  [sha256.Size]byte
	data   []byte
}

const es6Serial = 2000

func (s *es6Sequence) next() float64 {
	defer func() { s.idx++ }()
	switch {
	case s.idx < len(s.static):
		return math.Float64frombits(s.static[s.idx])
	case s.idx < len(s.static)+es6Serial:
		return math.Float64frombits(0x0010000000000000 + uint64(s.idx-len(s.static)))
	}
	// Each SHA-256 block yields four candidates; NaN/Inf/zero are rare, so
	// 64 draws bound the search (HISS-02).
	for range 64 {
		if len(s.data) == 0 {
			s.block = sha256.Sum256(s.block[:])
			s.data = s.block[:]
		}
		f := math.Float64frombits(binary.LittleEndian.Uint64(s.data))
		s.data = s.data[8:]
		if f != 0 && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
	panic("es6Sequence: 64 consecutive non-finite draws")
}

type checkpoint struct {
	sum   string
	lines int
	size  int64
}

func readLines(t *testing.T, name string) [][]string {
	t.Helper()
	f, err := os.Open(filepath.Join(vectors, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, strings.Fields(line))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func loadSequence(t *testing.T) (*es6Sequence, []checkpoint) {
	t.Helper()
	seq := &es6Sequence{}
	for _, f := range readLines(t, "numgen-static.txt") {
		u, err := strconv.ParseUint(f[0], 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		seq.static = append(seq.static, u)
	}
	rows := readLines(t, "numgen-sha256.txt")
	cps := make([]checkpoint, 0, len(rows))
	for _, f := range rows {
		lines, err := strconv.Atoi(f[1])
		if err != nil {
			t.Fatal(err)
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		cps = append(cps, checkpoint{sum: f[0], lines: lines, size: size})
	}
	if len(seq.static) != 168 || len(cps) != 6 {
		t.Fatalf("vector files: %d static values, %d checkpoints", len(seq.static), len(cps))
	}
	return seq, cps
}

// TestES6NumberFile rebuilds the leading lines of cyberphone's 100-million
// line ES6 number file ("<ieee hex>,<expected>\n") through Canonicalize and
// compares the SHA-256 checkpoints the upstream README publishes. 100k lines
// run always; 1M lines run without -short.
func TestES6NumberFile(t *testing.T) {
	t.Parallel()
	seq, cps := loadSequence(t)
	limit := 100000
	if !testing.Short() {
		limit = 1000000
	}
	h := sha256.New()
	var size int64
	line := make([]byte, 0, 64)
	next := 0
	for n := 1; n <= limit; n++ {
		f := seq.next()
		out, err := jcs.Canonicalize(strconv.AppendFloat(nil, f, 'e', 16, 64))
		if err != nil {
			t.Fatalf("line %d (%x): %v", n, math.Float64bits(f), err)
		}
		line = strconv.AppendUint(line[:0], math.Float64bits(f), 16)
		line = append(append(append(line, ','), out...), '\n')
		_, _ = h.Write(line)
		size += int64(len(line))
		if next < len(cps) && n == cps[next].lines {
			if got := hex.EncodeToString(h.Sum(nil)); got != cps[next].sum || size != cps[next].size {
				t.Fatalf("first %d lines: sha256 %s size %d, want %s size %d", n, got, size, cps[next].sum, cps[next].size)
			}
			next++
		}
	}
	if next < 3 {
		t.Fatalf("only %d checkpoints reached", next)
	}
}
