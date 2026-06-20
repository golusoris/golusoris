# Agent guide — testutil/prop/

Property-based testing helpers over `leanovate/gopter`: a deterministically
seeded `Properties` constructor (seed derived from the test name) so failures
reproduce without `-count=1`. Stateless test utility — **no fx wiring**.

## API

```go
props := prop.New(t)                     // *gopter.Properties, RNG seeded from t.Name()
props.Property("round-trip", goprop.ForAll(fn, gen.AnyString()))
props.TestingRun(t)

prop.Run(t, func(props *prop.Properties) { // New + setup + TestingRun in one call
    props.Property("idempotent", goprop.ForAll(fn, gen.SliceOf(gen.Int())))
})
```

`prop.Properties` is a type alias for `gopter.Properties`. Generators and
combinators come from gopter's own `gen` / `prop` sub-packages — apps import
those directly.

## Why leanovate/gopter

- Mature Go QuickCheck-style library with shrinking + composable generators;
  `New` only adds the deterministic seed, leaving the full gopter API intact.

## Notes

- Seed = FNV-64a of `t.Name()`, so each test always exercises the same input
  sequence; rename the test to reshuffle. RNG is `math/rand` (test-only, not
  crypto — `//nolint:gosec G404,G115`).
