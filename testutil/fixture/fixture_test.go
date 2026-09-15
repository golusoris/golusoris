// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fixture_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/testutil/fixture"
)

type account struct {
	ID    int    `csv:"id"`
	Email string `csv:"email"`
	Name  string `csv:"name"`
}

// TestLoad_validFile is the positive case: every row decodes into the
// declared struct in file order.
func TestLoad_validFile(t *testing.T) {
	t.Parallel()
	accounts, err := fixture.Load[account]("testdata/accounts.csv")
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	want := []account{
		{ID: 1, Email: "alice@example.com", Name: "Alice"},
		{ID: 2, Email: "bob@example.com", Name: "Bob"},
	}
	if len(accounts) != len(want) {
		t.Fatalf("Load() returned %d rows, want %d", len(accounts), len(want))
	}
	for i, w := range want {
		if accounts[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, accounts[i], w)
		}
	}
}

// TestLoad_missingColumn is the negative case: a header missing a column the
// struct declares must fail rather than zero-fill the field.
func TestLoad_missingColumn(t *testing.T) {
	t.Parallel()
	_, err := fixture.Load[account]("testdata/missing_column.csv")
	if err == nil {
		t.Fatal("Load() = nil, want error for header missing a declared column")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("Load() error = %q, want it to name the missing column", err.Error())
	}
}

// TestLoad_emptyFile is the negative case for a file with no header at all.
func TestLoad_emptyFile(t *testing.T) {
	t.Parallel()
	_, err := fixture.Load[account]("testdata/empty.csv")
	if err == nil {
		t.Fatal("Load() = nil, want error for empty file")
	}
}

// TestLoad_missingFile is the negative case for a path that does not exist.
func TestLoad_missingFile(t *testing.T) {
	t.Parallel()
	_, err := fixture.Load[account]("testdata/does_not_exist.csv")
	if err == nil {
		t.Fatal("Load() = nil, want error for missing file")
	}
}

// TestMustLoad_validFile is the positive case for the t.Fatal-on-error
// variant: it returns the same rows as Load without failing the test.
func TestMustLoad_validFile(t *testing.T) {
	t.Parallel()
	accounts := fixture.MustLoad[account](t, "testdata/accounts.csv")
	if len(accounts) != 2 {
		t.Fatalf("MustLoad() returned %d rows, want 2", len(accounts))
	}
}
