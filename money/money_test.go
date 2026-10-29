// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package money_test

import (
	"errors"
	"testing"

	"github.com/golusoris/golusoris/money"
)

func TestNew_string(t *testing.T) {
	t.Parallel()
	cases := []struct {
		amount   int64
		currency string
		want     string
	}{
		{999, "USD", "9.99 USD"},
		{100, "EUR", "1.00 EUR"},
		{0, "GBP", "0.00 GBP"},
		{150, "JPY", "150 JPY"}, // zero-decimal
		{-500, "USD", "-5.00 USD"},
		{1234, "KWD", "1.234 KWD"},
		{1234, "BHD", "1.234 BHD"},
		{1234, "JOD", "1.234 JOD"},
		{1234, "OMR", "1.234 OMR"},
		{1234, "TND", "1.234 TND"},
		{1234, "LYD", "1.234 LYD"},
		{1234, "IQD", "1.234 IQD"},
		{1234, "IDR", "12.34 IDR"},
		{1234, "COP", "12.34 COP"},
		{-1234, "KWD", "-1.234 KWD"},
		{0, "KWD", "0.000 KWD"},
		{5, "KWD", "0.005 KWD"},
		{1234, "kwd", "1.234 KWD"},
		{12345, "CLF", "1.2345 CLF"},
		{1234, "ZZZ", "12.34 ZZZ"},
		{1234, "US", "12.34 US"},
		{1234, "USDD", "12.34 USDD"},
		{1234, "", "12.34 "},
	}
	for _, c := range cases {
		m := money.New(c.amount, c.currency)
		if got := m.String(); got != c.want {
			t.Errorf("New(%d,%s).String() = %q, want %q", c.amount, c.currency, got, c.want)
		}
	}
}

func TestFromMajor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		major    float64
		currency string
		want     int64
	}{
		{9.99, "USD", 999},
		{150, "JPY", 150},
		{1.2345, "KWD", 1235},
		{-1.2345, "KWD", -1235},
		{0, "KWD", 0},
		{0.0005, "KWD", 1},
		{1.2345, "BHD", 1235},
		{1.2345, "JOD", 1235},
		{1.2345, "OMR", 1235},
		{1.2345, "TND", 1235},
		{1.2345, "LYD", 1235},
		{1.2345, "IQD", 1235},
		{12.34, "IDR", 1234},
		{12.34, "COP", 1234},
		{1.2345, "kwd", 1235},
		{1.2345, "CLF", 12345},
		{12.34, "ZZZ", 1234},
		{12.34, "US", 1234},
		{12.34, "USDD", 1234},
		{12.34, "", 1234},
	}
	for _, c := range cases {
		m := money.FromMajor(c.major, c.currency)
		if m.Amount != c.want {
			t.Errorf("FromMajor(%g,%s).Amount = %d, want %d", c.major, c.currency, m.Amount, c.want)
		}
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()
	a := money.New(100, "USD")
	b := money.New(50, "USD")
	got, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.Amount != 150 {
		t.Fatalf("expected 150, got %d", got.Amount)
	}
}

func TestSub(t *testing.T) {
	t.Parallel()
	got, err := money.New(100, "USD").Sub(money.New(30, "USD"))
	if err != nil {
		t.Fatalf("Sub: %v", err)
	}
	if got.Amount != 70 {
		t.Fatalf("expected 70, got %d", got.Amount)
	}
}

func TestMul(t *testing.T) {
	t.Parallel()
	// 10% of $9.99 = $1.00 (rounded from $0.999)
	tax := money.New(999, "USD").Mul(0.10)
	if tax.Amount != 100 {
		t.Fatalf("expected 100, got %d", tax.Amount)
	}
}

func TestNeg_Abs(t *testing.T) {
	t.Parallel()
	m := money.New(500, "USD")
	if !m.Neg().IsNeg() {
		t.Fatal("Neg() should be negative")
	}
	if m.Neg().Abs().Amount != 500 {
		t.Fatal("Abs() of negative should be positive")
	}
}

func TestMajorUnits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		amount   int64
		currency string
		want     float64
	}{
		{1234, "USD", 12.34},
		{150, "JPY", 150},
		{1234, "KWD", 1.234},
		{-1234, "KWD", -1.234},
		{0, "KWD", 0},
		{5, "KWD", 0.005},
		{1234, "BHD", 1.234},
		{1234, "JOD", 1.234},
		{1234, "OMR", 1.234},
		{1234, "TND", 1.234},
		{1234, "LYD", 1.234},
		{1234, "IQD", 1.234},
		{1234, "IDR", 12.34},
		{1234, "COP", 12.34},
		{1234, "kwd", 1.234},
		{12345, "CLF", 1.2345},
		{1234, "ZZZ", 12.34},
		{1234, "US", 12.34},
		{1234, "USDD", 12.34},
		{1234, "", 12.34},
	}
	for _, c := range cases {
		m := money.New(c.amount, c.currency)
		if got := m.MajorUnits(); got != c.want {
			t.Errorf("New(%d,%s).MajorUnits() = %g, want %g", c.amount, c.currency, got, c.want)
		}
	}
}

func TestSameCurrency(t *testing.T) {
	t.Parallel()
	if !money.New(1, "USD").SameCurrency(money.New(2, "USD")) {
		t.Fatal("same currency should report true")
	}
	if money.New(1, "USD").SameCurrency(money.New(1, "EUR")) {
		t.Fatal("different currencies should report false")
	}
}

func TestCurrencyMismatch_errors(t *testing.T) {
	t.Parallel()
	if _, err := money.New(1, "USD").Add(money.New(1, "EUR")); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Add: expected ErrCurrencyMismatch, got %v", err)
	}
	if _, err := money.New(1, "USD").Sub(money.New(1, "EUR")); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Sub: expected ErrCurrencyMismatch, got %v", err)
	}
}
