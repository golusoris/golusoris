// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package fixture loads typed test fixtures from testdata CSV files into
// slices of structs, backed by jszwec/csvutil.
//
// The first line of the file is treated as a header and matched against the
// "csv" struct tag (or field name) of T for every exported field; a header
// missing a column T declares is reported as an error instead of silently
// zero-filling the field.
//
// Usage:
//
//	type Account struct {
//	    ID    int    `csv:"id"`
//	    Email string `csv:"email"`
//	}
//
//	accounts, err := fixture.Load[Account]("testdata/accounts.csv")
//
//	// In a test, where a broken fixture should fail the test immediately:
//	accounts := fixture.MustLoad[Account](t, "testdata/accounts.csv")
package fixture

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/jszwec/csvutil"
)

// MaxRows bounds the number of data rows Load will decode from a single CSV
// fixture (HISS-02: scalar loop bound). A hand-written test fixture never
// approaches this, so Load fails closed on anything larger instead of
// growing the result slice without limit.
const MaxRows = 10_000

// Load reads path as CSV and decodes every data row into a T, using the
// first line as the header. It returns an error if the file has no header
// row, if the header is missing a column T declares (via "csv" struct tags
// or field names), if a row fails to decode, or if the file has more than
// MaxRows data rows.
func Load[T any](path string) ([]T, error) {
	// #nosec G304 -- path is a test's own testdata literal; this package is test tooling, never reached by a request path.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fixture: read %s: %w", path, err)
	}

	dec, err := csvutil.NewDecoder(csv.NewReader(bytes.NewReader(data)))
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("fixture: %s: empty file (no header row)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("fixture: %s: %w", path, err)
	}
	dec.DisallowMissingColumns = true

	rows, err := decodeRows[T](dec, MaxRows)
	if err != nil {
		return nil, fmt.Errorf("fixture: %s: %w", path, err)
	}
	return rows, nil
}

// MustLoad is [Load], failing the test via t.Fatal instead of returning an
// error. Use it in test setup where a broken fixture should stop the test
// immediately rather than propagate an error the caller must check.
func MustLoad[T any](t *testing.T, path string) []T {
	t.Helper()
	rows, err := Load[T](path)
	if err != nil {
		t.Fatalf("fixture: MustLoad: %v", err)
	}
	return rows
}

// decodeRows drives dec.Decode into fresh T values for up to maxRows data
// rows (HISS-02: scalar loop bound), then issues one further Decode call to
// tell a file that lands exactly on the bound apart from one that overflows
// it, rather than silently truncating the extra rows. Extracted from Load so
// the bound itself is unit-testable without a MaxRows-sized fixture file.
func decodeRows[T any](dec *csvutil.Decoder, maxRows int) ([]T, error) {
	rows := make([]T, 0, maxRows)
	for range maxRows {
		var v T
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				return rows, nil
			}
			return nil, fmt.Errorf("row %d: %w", len(rows)+1, err)
		}
		rows = append(rows, v)
	}

	var v T
	switch err := dec.Decode(&v); {
	case errors.Is(err, io.EOF):
		return rows, nil
	case err != nil:
		return nil, fmt.Errorf("row %d: %w", len(rows)+1, err)
	default:
		return nil, fmt.Errorf("exceeds row bound of %d", maxRows)
	}
}
