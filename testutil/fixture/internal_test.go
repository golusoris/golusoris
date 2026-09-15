// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fixture

import (
	"bytes"
	"encoding/csv"
	"testing"

	"github.com/jszwec/csvutil"
)

type widget struct {
	ID int `csv:"id"`
}

// newTestDecoder builds a csvutil.Decoder over an in-memory CSV so
// decodeRows can be exercised at a small maxRows without a MaxRows-sized
// fixture file.
func newTestDecoder(t *testing.T, data string) *csvutil.Decoder {
	t.Helper()
	dec, err := csvutil.NewDecoder(csv.NewReader(bytes.NewReader([]byte(data))))
	if err != nil {
		t.Fatalf("newTestDecoder: %v", err)
	}
	return dec
}

// TestDecodeRows_underBound is the positive case: fewer data rows than
// maxRows decode in full.
func TestDecodeRows_underBound(t *testing.T) {
	t.Parallel()
	dec := newTestDecoder(t, "id\n1\n2\n")
	rows, err := decodeRows[widget](dec, 5)
	if err != nil {
		t.Fatalf("decodeRows() = %v, want nil", err)
	}
	if len(rows) != 2 {
		t.Fatalf("decodeRows() returned %d rows, want 2", len(rows))
	}
}

// TestDecodeRows_atBound is the boundary case: exactly maxRows data rows
// must decode successfully rather than being rejected as an overflow.
func TestDecodeRows_atBound(t *testing.T) {
	t.Parallel()
	dec := newTestDecoder(t, "id\n1\n2\n")
	rows, err := decodeRows[widget](dec, 2)
	if err != nil {
		t.Fatalf("decodeRows() = %v, want nil", err)
	}
	if len(rows) != 2 {
		t.Fatalf("decodeRows() returned %d rows, want 2", len(rows))
	}
}

// TestDecodeRows_overBound is the negative case (HISS-02): one data row past
// maxRows must fail closed instead of growing the result slice without
// limit.
func TestDecodeRows_overBound(t *testing.T) {
	t.Parallel()
	dec := newTestDecoder(t, "id\n1\n2\n3\n")
	rows, err := decodeRows[widget](dec, 2)
	if err == nil {
		t.Fatalf("decodeRows() = %v rows, nil error; want bound-exceeded error", rows)
	}
	if rows != nil {
		t.Errorf("decodeRows() rows = %v, want nil on error", rows)
	}
}
