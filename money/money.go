// Package money provides a currency-aware money type stored as integer
// minor units (cents, pence, …) to avoid floating-point rounding errors.
//
// Currency codes follow ISO 4217. Common zero-decimal currencies (JPY, KRW,
// …) are listed in [ZeroDecimalCurrencies].
//
// Usage:
//
//	price := money.New(999, "USD")  // $9.99
//	tax   := price.Mul(0.10)        // $0.999 → rounded to $1.00
//	total := price.Add(tax)         // $10.99
//	fmt.Println(total.String())     // "10.99 USD"
package money

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Money is an immutable currency-aware value. Amount is in minor units
// (e.g. cents for USD, the smallest indivisible unit for the currency).
type Money struct {
	Amount   int64
	Currency string // ISO 4217 e.g. "USD", "EUR", "JPY"
}

// New returns a Money value. amount is in minor units.
func New(amount int64, currency string) Money {
	return Money{Amount: amount, Currency: strings.ToUpper(currency)}
}

// FromMajor converts a major-unit amount (e.g. 9.99 dollars) to minor units.
// Rounds to the nearest minor unit.
func FromMajor(major float64, currency string) Money {
	divisor := float64(minorUnitDivisor(strings.ToUpper(currency)))
	return New(int64(math.Round(major*divisor)), currency)
}

// MajorUnits returns the amount in major units as a float64.
func (m Money) MajorUnits() float64 {
	return float64(m.Amount) / float64(minorUnitDivisor(m.Currency))
}

// Add returns m + other. Panics if currencies differ.
func (m Money) Add(other Money) Money {
	m.mustSameCurrency(other)
	return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}
}

// Sub returns m - other. Panics if currencies differ.
func (m Money) Sub(other Money) Money {
	m.mustSameCurrency(other)
	return Money{Amount: m.Amount - other.Amount, Currency: m.Currency}
}

// Mul multiplies by a factor and rounds to the nearest minor unit.
func (m Money) Mul(factor float64) Money {
	return Money{Amount: int64(math.Round(float64(m.Amount) * factor)), Currency: m.Currency}
}

// Neg returns the negated value.
func (m Money) Neg() Money { return Money{Amount: -m.Amount, Currency: m.Currency} }

// Abs returns the absolute value.
func (m Money) Abs() Money {
	if m.Amount < 0 {
		return Money{Amount: -m.Amount, Currency: m.Currency}
	}
	return m
}

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.Amount == 0 }

// IsNeg reports whether the amount is negative.
func (m Money) IsNeg() bool { return m.Amount < 0 }

// SameCurrency reports whether m and other use the same currency.
func (m Money) SameCurrency(other Money) bool {
	return m.Currency == other.Currency
}

// String returns "12.34 USD" for non-zero-decimal currencies, "1234 JPY" for zero-decimal.
func (m Money) String() string {
	divisor := minorUnitDivisor(m.Currency)
	if divisor == 1 {
		return fmt.Sprintf("%d %s", m.Amount, m.Currency)
	}
	major := m.Amount / int64(divisor)
	minor := m.Amount % int64(divisor)
	if minor < 0 {
		minor = -minor
	}
	decimals := len(strconv.Itoa(divisor)) - 1
	return fmt.Sprintf("%d.%0*d %s", major, decimals, minor, m.Currency)
}

func (m Money) mustSameCurrency(other Money) {
	if m.Currency != other.Currency {
		panic(fmt.Sprintf("money: currency mismatch %s vs %s", m.Currency, other.Currency))
	}
}

// minorUnitDivisor returns the number of minor units per major unit.
// 100 for most currencies (USD, EUR, GBP…); 1 for zero-decimal currencies.
func minorUnitDivisor(currency string) int {
	if _, ok := ZeroDecimalCurrencies[currency]; ok {
		return 1
	}
	return 100
}

// ZeroDecimalCurrencies lists ISO 4217 currencies with no minor unit.
// Source: Stripe zero-decimal currency list.
var ZeroDecimalCurrencies = map[string]struct{}{
	"BIF": {}, "CLP": {}, "DJF": {}, "GNF": {}, "ISK": {},
	"JPY": {}, "KMF": {}, "KRW": {}, "MGA": {}, "PYG": {},
	"RWF": {}, "UGX": {}, "UYI": {}, "VND": {}, "VUV": {},
	"XAF": {}, "XOF": {}, "XPF": {},
}
