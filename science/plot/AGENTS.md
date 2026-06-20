# Agent guide — science/plot/

Thin chart helpers over gonum/plot (line/scatter → PNG). Stateless utility —
**no fx wiring**. Own go.mod sub-module; import directly:
`github.com/golusoris/golusoris/science/plot`.

## API

```go
c, err := plot.New(title, xLabel, yLabel)
c.AddLine("label", xs, ys)              // xs/ys must be equal length
c.AddScatter("label", xs, ys)
c.WritePNG(w, widthPx, heightPx)        // to an io.Writer
c.SavePNG(path, widthPx, heightPx)      // to a file
raw := c.Raw()                          // *gonum/plot.Plot for advanced use
```

## Why gonum.org/v1/plot

Pure-Go plotting that pairs with `science/numerical`; `Raw()` exposes the
underlying `*plot.Plot` so callers can drop down to the full gonum API.

## Notes

- Separate go.mod because gonum/plot pulls font + image rendering libs (~20 MB).
- Pixel dimensions assume 96 DPI (`px * vg.Inch / 96`).
- `AddLine`/`AddScatter` error on `len(xs) != len(ys)` rather than panicking.
