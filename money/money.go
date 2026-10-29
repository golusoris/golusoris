// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package money provides a currency-aware money type stored as integer
// minor units (cents, pence, …) to avoid floating-point rounding errors.
//
// Currency amounts use ISO 4217 minor units via github.com/bojanz/currency.
// Unknown or invalid codes use two decimals.
//
// Usage:
//
//	price := money.New(999, "USD")  // $9.99
//	tax   := price.Mul(0.10)        // $0.999 → rounded to $1.00
//	total := price.Add(tax)         // $10.99
//	fmt.Println(total.String())     // "10.99 USD"
package money

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/bojanz/currency"
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

// ErrCurrencyMismatch is returned by [Money.Add] and [Money.Sub] when the
// operands use different currencies.
var ErrCurrencyMismatch = errors.New("money: currency mismatch")

// Add returns m + other, or [ErrCurrencyMismatch] if currencies differ.
func (m Money) Add(other Money) (Money, error) {
	if err := m.checkSameCurrency(other); err != nil {
		return Money{}, err
	}
	return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}, nil
}

// Sub returns m - other, or [ErrCurrencyMismatch] if currencies differ.
func (m Money) Sub(other Money) (Money, error) {
	if err := m.checkSameCurrency(other); err != nil {
		return Money{}, err
	}
	return Money{Amount: m.Amount - other.Amount, Currency: m.Currency}, nil
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

// String formats the amount with the currency's minor-unit decimal places,
// e.g. "12.34 USD", "1.234 KWD", or "1234 JPY".
func (m Money) String() string {
	decimals := minorUnitScale(m.Currency)
	if decimals == 0 {
		return fmt.Sprintf("%d %s", m.Amount, m.Currency)
	}
	divisor := int64(math.Pow10(decimals))
	major := m.Amount / divisor
	minor := m.Amount % divisor
	if minor < 0 {
		minor = -minor
	}
	return fmt.Sprintf("%d.%0*d %s", major, decimals, minor, m.Currency)
}

func (m Money) checkSameCurrency(other Money) error {
	if m.Currency != other.Currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.Currency, other.Currency)
	}
	return nil
}

// minorUnitDivisor returns the number of minor units per major unit.
func minorUnitDivisor(code string) int {
	return int(math.Pow10(minorUnitScale(code)))
}

func minorUnitScale(code string) int {
	digits, ok := currency.GetDigits(strings.ToUpper(code))
	if !ok {
		return 2
	}
	return int(digits)
}

// ZeroDecimalCurrencies lists ISO 4217 currencies with no minor unit.
// Source: Stripe zero-decimal currency list.
//
// Deprecated: amounts use ISO 4217 minor units via github.com/bojanz/currency; this list is no longer consulted.
var ZeroDecimalCurrencies = map[string]struct{}{
	"BIF": {}, "CLP": {}, "DJF": {}, "GNF": {}, "ISK": {},
	"JPY": {}, "KMF": {}, "KRW": {}, "MGA": {}, "PYG": {},
	"RWF": {}, "UGX": {}, "UYI": {}, "VND": {}, "VUV": {},
	"XAF": {}, "XOF": {}, "XPF": {},
}
