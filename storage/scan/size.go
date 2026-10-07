// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scan

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// sizeUnits maps a (case-insensitive) suffix to its byte multiplier. Binary
// (KiB) and decimal/SI (kB, MB) forms both resolve to powers of 1024 here —
// upload limits are conventionally quoted in 2^10 steps and matching clamd's
// StreamMaxLength (which is binary) avoids an off-by-4% rejection surprise.
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"tib", 1 << 40},
	{"tb", 1 << 40},
	{"gib", 1 << 30},
	{"gb", 1 << 30},
	{"mib", 1 << 20},
	{"mb", 1 << 20},
	{"kib", 1 << 10},
	{"kb", 1 << 10},
	{"b", 1},
}

// parseSize converts a human-readable size ("25MB", "1024", "5 GiB") to bytes.
// A bare number is interpreted as bytes. Empty string yields (0, nil) — the
// caller treats 0 as "no limit".
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	for _, u := range sizeUnits {
		if !strings.HasSuffix(s, u.suffix) {
			continue
		}
		num := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
		return parseScaledSize(s, num, u.mult)
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("storage/scan: parse size %q: %w", s, err)
	}
	if val < 0 {
		return 0, fmt.Errorf("storage/scan: negative size %q", s)
	}
	return val, nil
}

func parseScaledSize(input, number string, multiplier int64) (int64, error) {
	value, ok := new(big.Rat).SetString(number)
	if !ok {
		return 0, fmt.Errorf("storage/scan: parse size %q: invalid number", input)
	}
	if value.Sign() < 0 {
		return 0, fmt.Errorf("storage/scan: negative size %q", input)
	}
	value.Mul(value, new(big.Rat).SetInt64(multiplier))
	bytes := new(big.Int).Quo(value.Num(), value.Denom())
	if !bytes.IsInt64() {
		return 0, fmt.Errorf("storage/scan: size %q overflows int64 bytes", input)
	}
	result := bytes.Int64()
	if value.Sign() > 0 && result == 0 {
		return 0, fmt.Errorf("storage/scan: size %q is below one byte", input)
	}
	return result, nil
}
