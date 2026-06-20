# Agent guide — docs/docx/

Read/write DOCX files via template substitution over
`nguyenthenguyen/docx` (pure Go, no LibreOffice). Stateless utility —
**no fx wiring**. Apps import it directly.

## API

```go
doc, err := docx.Open(ctx, "template.docx")     // or OpenReader(ctx, r, size)
doc.Replace("{{name}}", "Alice", -1)            // -1 = replace all
doc.ReplaceHeader("{{co}}", "Acme")
doc.ReplaceFooter("{{page}}", "1")
err = doc.Save("out.docx")                       // or Write(w) / Bytes()
err = doc.Close()                                // releases the zip reader
```

## Why nguyenthenguyen/docx

- Pure-Go placeholder substitution; no LibreOffice/headless-office process to
  shell out to. Workflow is "load a template, replace placeholders, save".

## Notes

- Template-driven only — generate documents from a `.docx` you author, not from
  arbitrary structured content. `New` produces a minimal single-paragraph base.
- `OpenReader` needs a seekable `io.ReaderAt` plus its `size`.
- Always `Close` to release the underlying zip reader.
