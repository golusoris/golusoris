// Package bio provides bioinformatics helpers — sequence handling, FASTA/FASTQ
// parsing, and basic alignment utilities using biogo/biogo.
//
// This is a separate go.mod sub-module because biogo's dependency graph is
// large and specialised.
// Import directly: github.com/golusoris/golusoris/science/bio
package bio

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/biogo/biogo/alphabet"
	"github.com/biogo/biogo/seq/linear"
)

// Sequence holds a named biological sequence.
type Sequence struct {
	Name string
	Seq  string
}

// DNASeq wraps a string as a biogo linear.Seq for DNA.
func DNASeq(name, seq string) *linear.Seq {
	s := linear.NewSeq(name, []alphabet.Letter(seq), alphabet.DNA)
	return s
}

// RNASeq wraps a string as a biogo linear.Seq for RNA.
func RNASeq(name, seq string) *linear.Seq {
	s := linear.NewSeq(name, []alphabet.Letter(seq), alphabet.RNA)
	return s
}

// ParseFASTA reads FASTA records from r.
// Each record starts with a ">" header line followed by sequence lines.
func ParseFASTA(r io.Reader) ([]Sequence, error) {
	var seqs []Sequence
	var cur *Sequence
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, ">") {
			if cur != nil {
				seqs = append(seqs, *cur)
			}
			cur = &Sequence{Name: strings.TrimPrefix(line, ">")}
		} else if cur != nil {
			cur.Seq += strings.ToUpper(line)
		}
	}
	if cur != nil {
		seqs = append(seqs, *cur)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("bio: fasta scan: %w", err)
	}
	return seqs, nil
}

// ReverseComplement returns the reverse complement of a DNA sequence.
func ReverseComplement(seq string) string {
	comp := map[rune]rune{
		'A': 'T', 'T': 'A', 'G': 'C', 'C': 'G',
		'a': 't', 't': 'a', 'g': 'c', 'c': 'g',
		'N': 'N', 'n': 'n',
	}
	runes := []rune(seq)
	// reverse
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	// complement
	for i, r := range runes {
		if c, ok := comp[r]; ok {
			runes[i] = c
		}
	}
	return string(runes)
}

// GCContent returns the GC fraction (0–1) of a DNA/RNA sequence.
func GCContent(seq string) float64 {
	if len(seq) == 0 {
		return 0
	}
	gc := 0
	for _, c := range strings.ToUpper(seq) {
		if c == 'G' || c == 'C' {
			gc++
		}
	}
	return float64(gc) / float64(len(seq))
}
