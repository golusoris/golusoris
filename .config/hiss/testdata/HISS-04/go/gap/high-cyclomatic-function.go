// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds a short function with high cyclomatic complexity.
package p

// Classify is far inside the 75-LOC cap the scanner measures, yet its twelve
// independent branches put it past the cyclomatic cap of 10. Only the length
// half of HISS-04 reaches the scanner; the three other caps are golangci-lint's.
func Classify(code int) string {
	switch {
	case code < 0:
		return "negative"
	case code == 0:
		return "zero"
	case code < 10:
		return "tiny"
	case code < 100:
		return "small"
	case code < 1000:
		return "medium"
	case code < 10000:
		return "large"
	case code < 100000:
		return "huge"
	case code < 1000000:
		return "vast"
	case code%2 == 0:
		return "even-overflow"
	case code%3 == 0:
		return "triple-overflow"
	case code%5 == 0:
		return "quintuple-overflow"
	default:
		return "overflow"
	}
}
