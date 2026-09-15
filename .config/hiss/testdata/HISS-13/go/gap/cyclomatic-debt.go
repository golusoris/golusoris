// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p adds debt the ratchet cannot count.
package p

// Classify is real HISS-04 debt -- twelve branches against a cap of ten -- but
// the ratchet's reach is exactly the scanner's rule set, and the cyclomatic cap
// belongs to golangci-lint, whose findings never reach .standards-baseline.json.
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
