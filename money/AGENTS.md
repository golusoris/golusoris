# Agent guide — money/

Currency-aware money type stored as integer minor units (cents, pence, …)
to avoid floating-point rounding errors.

## API

```go
price := money.New(999, "USD")   // 999 cents = $9.99
tax   := price.Mul(0.10)         // rounded to nearest cent
total := price.Add(tax)
fmt.Println(total.String())      // "10.99 USD"

m := money.FromMajor(9.99, "USD")  // float64 major → minor units
```

## Operations

| Method | Result |
|---|---|
| `Add(other)` | sum (panics on currency mismatch) |
| `Sub(other)` | difference |
| `Mul(factor float64)` | multiply + round |
| `Neg()` / `Abs()` | negate / absolute |
| `MajorUnits()` | float64 major-unit representation |
| `String()` | "12.34 USD" or "1234 JPY" |

## Zero-decimal currencies

`ZeroDecimalCurrencies` lists ISO 4217 currencies with no minor unit (JPY,
KRW, VND, …). `New(150, "JPY").String()` returns `"150 JPY"`.

## Don't

- Don't store `Money` as float64 — rounding errors accumulate. Use minor units.
- Don't compare `.MajorUnits()` for equality — use `m.Amount == other.Amount`.
- Don't cross currencies without checking `SameCurrency` first.
