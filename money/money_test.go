package money_test

import (
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
	m := money.FromMajor(9.99, "USD")
	if m.Amount != 999 {
		t.Fatalf("expected 999, got %d", m.Amount)
	}
}

func TestAdd(t *testing.T) {
	t.Parallel()
	a := money.New(100, "USD")
	b := money.New(50, "USD")
	got := a.Add(b)
	if got.Amount != 150 {
		t.Fatalf("expected 150, got %d", got.Amount)
	}
}

func TestSub(t *testing.T) {
	t.Parallel()
	got := money.New(100, "USD").Sub(money.New(30, "USD"))
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
	m := money.New(1234, "USD")
	if m.MajorUnits() != 12.34 {
		t.Fatalf("expected 12.34, got %f", m.MajorUnits())
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

func TestCurrencyMismatch_panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on currency mismatch")
		}
	}()
	money.New(1, "USD").Add(money.New(1, "EUR"))
}
