# Agent guide — science/numerical/

Linear-algebra and statistics helpers over gonum. Stateless utility — **no fx
wiring**. Own go.mod sub-module; import directly:
`github.com/golusoris/golusoris/science/numerical`.

## API

```go
m := numerical.NewDense(r, c, data)     // wraps mat.NewDense
numerical.Mean(vals); numerical.StdDev(vals); numerical.Variance(vals)
numerical.Dot(a, b)                     // equal-length slices
numerical.Norm2(v)                      // L2 norm
```

## Why gonum.org/v1/gonum

The canonical Go numerics stack (BLAS-backed mat, stat). The wrapper lets apps
call common ops without importing `mat`/`stat` directly.

## Notes

- Separate go.mod because gonum pulls large test-data assets that would bloat the
  main framework's tree. Activate via go.work or your app's go.mod.
- `StdDev`/`Variance` are sample statistics (n−1); pass nil weights through gonum.
- `Dot`/`Norm2` panic on length mismatch (gonum contract) — validate lengths.
