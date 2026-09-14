- **BREAKING** (`money`): `Money.Add` / `Money.Sub` return `(Money, error)` instead of panicking on a currency mismatch; new sentinel `money.ErrCurrencyMismatch` (HISS-07).

  ```go
  // before
  total := price.Add(tax) // panics if currencies differ
  // after
  total, err := price.Add(tax)
  if errors.Is(err, money.ErrCurrencyMismatch) { /* … */ }
  ```

- **BREAKING** (`plugin`): `Registry.Register` returns `error` (`plugin.ErrDuplicate` on a repeat key) instead of panicking; `Registry.MustGet` is replaced by `Registry.Lookup(key) (T, error)` (`plugin.ErrNotRegistered` on a miss). `MustRegister` and `Get` are unchanged (HISS-07).

  ```go
  // before
  Providers.Register("stripe", impl) // panics on duplicate
  p := Providers.MustGet("stripe")   // panics on miss
  // after
  if err := Providers.Register("stripe", impl); err != nil { return err }
  p, err := Providers.Lookup("stripe")
  ```
