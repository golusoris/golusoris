<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — pdf/parse/

Extract metadata, validate, merge, and optimize PDF files via pdfcpu
(pure Go, **no CGO**). Stateless utility — **no fx wiring**. Apps import it
directly.

## API

```go
// r is io.ReadSeeker; file variant: InfoFile(ctx, path).
info, err := parse.Info(ctx, r, "report.pdf")
// info.Pages, info.Title, info.Author, info.Tagged, info.Linearized, ...

t := parse.ParseTime(info.CreationDate) // invalid input returns zero time

err = parse.Validate(ctx, r) // file variant: ValidateFile(ctx, path)
err = parse.Merge(ctx, []string{"a.pdf", "b.pdf"}, "out.pdf")
err = parse.Optimize(ctx, "src.pdf", "dst.pdf") // drop redundant objects
```

`Info` takes `io.ReadSeeker` (pdfcpu seeks xref); `fileName` is used in
error messages only.

## Why pdfcpu

- Pure-Go PDF toolkit (info / validate / merge / optimize) with no CGO and no
 external `pdftk`/`qpdf` binary — keeps build static and rootless.

## Notes

- Read-side only: metadata + structural ops. No text-layer extraction or
 rendering here.
- `Merge` is no-op on empty input slice. `Optimize` writes output
 `0o600`.
- Context cancellation crosses each reader, seek, and writer boundary.
- pdfcpu CPU work between I/O boundaries cannot be preempted; use process
 isolation when hostile PDFs require hard wall-clock termination.
- Untrusted PDFs are attack surface — `Validate` before processing
 third-party files.
