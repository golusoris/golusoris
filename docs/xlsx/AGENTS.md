# Agent guide — docs/xlsx/

Read/write XLSX spreadsheets via thin helpers over `xuri/excelize/v2`.
Stateless utility — **no fx wiring**. Apps import it directly.

## API

```go
f := xlsx.New()                                  // in-memory workbook
f.SetHeader("Sheet1", []string{"Name", "Score"})
f.AppendRow("Sheet1", []any{"Alice", 99})
f.SetCell("Sheet1", "A1", "x"); v, _ := f.GetCell("Sheet1", "A1")
err := f.SaveAs("report.xlsx")                    // or WriteTo(w)

f, err := xlsx.Open(ctx, "report.xlsx")           // or OpenReader(ctx, r)
rows, err := f.ReadRows("Sheet1")                 // [][]string
defer f.Close()

rows, err := xlsx.ReadRows(ctx, path, "Sheet1")   // open+read+close convenience
raw := f.Raw()                                    // *excelize.File for advanced use
```

## Why xuri/excelize/v2

- Most complete pure-Go XLSX library (styles, formulas, streaming) — no CGO,
  no external spreadsheet engine.

## Notes

- `AppendRow` writes at `len(GetRows)+1` — trailing blank rows shift the index.
- `Close` releases excelize resources on opened/read files.
- Drop to `Raw()` for styling, charts, and streaming writers not surfaced here.
