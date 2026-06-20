# Agent guide — pdf/parse/

Extract metadata, validate, merge, and optimize PDF files via pdfcpu
(pure Go, **no CGO**). Stateless utility — **no fx wiring**. Apps import it
directly.

## API

```go
info, err := parse.Info(ctx, r, "report.pdf")     // io.ReadSeeker; or InfoFile(ctx, path)
// info.Pages, info.Title, info.Author, info.Tagged, info.Linearized, info.HasForm, ...

t := parse.ParseTime(info.CreationDate)            // "D:YYYYMMDDHHmmSS" -> time.Time (zero on fail)

err = parse.Validate(ctx, r)                       // or ValidateFile(ctx, path)
err = parse.Merge(ctx, []string{"a.pdf", "b.pdf"}, "out.pdf")
err = parse.Optimize(ctx, "src.pdf", "dst.pdf")    // drop redundant objects
```

`Info` takes an `io.ReadSeeker` (pdfcpu seeks the xref); `fileName` is used in
error messages only.

## Why pdfcpu

- Pure-Go PDF toolkit (info / validate / merge / optimize) with no CGO and no
  external `pdftk`/`qpdf` binary — keeps the build static and rootless.

## Notes

- Read-side only: metadata + structural ops. No text-layer extraction or
  rendering here.
- `Merge` is a no-op on an empty input slice. `Optimize` writes the output
  `0o600`.
- Untrusted PDFs are an attack surface — `Validate` before processing
  third-party files.
