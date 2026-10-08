// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package bio provides bounded FASTA parsing and basic DNA/RNA sequence
// helpers.
//
// Import directly: github.com/golusoris/golusoris/science/bio
package bio

import (
	"strings"
)

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
