# Agent guide — docs/epub/

Generate EPUB 3.0 files (with EPUB 2.0 ToC for compatibility) via a thin
wrapper over `bmaupin/go-epub`. Stateless utility — **no fx wiring**. Apps
import it directly.

## API

```go
b := epub.New("My Book")
b.SetAuthor("Alice")
b.SetLang("en")                                  // default "en"
b.SetDescription("...")
css, _ := b.AddCSS("style.css", "")              // returns internal path
img, _ := b.AddImage("cover.png", "")
b.SetCover(img, css)
_, err := b.AddSection("<h1>Ch 1</h1>", "Chapter 1")     // "" title omits from ToC
_, err = b.AddSectionWithCSS(body, title, css)
err = b.Write("mybook.epub")                     // or WriteToWriter(w)
```

## Why bmaupin/go-epub

- Pure-Go EPUB 3 writer with embedded images/CSS and automatic ToC generation.

## Notes

- `body` is HTML; the package does not sanitize it — escape untrusted input.
- `WriteToWriter` round-trips through an `os.CreateTemp` file (go-epub only
  writes to a path), then streams + removes it.
