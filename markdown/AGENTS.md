# Agent guide — markdown/

Renders Markdown to HTML using goldmark with GFM extensions (tables,
strikethrough, task lists, linkify), footnotes, typographer (smart quotes),
and auto-heading IDs.

## API

```go
html, err := markdown.Render([]byte(src))     // []byte → []byte
html := markdown.RenderString(src)            // panics on error — for static/trusted content
err  := markdown.RenderTo(&buf, []byte(src))  // write to existing buffer
```

## Security

Output is **not sanitized**. For user-generated content, pipe through a
sanitizer (e.g. microcosm-cc/bluemonday) before writing to HTTP responses:

```go
out, _ := markdown.Render(src)
safe := bluemonday.UGCPolicy().SanitizeBytes(out)
```

## Don't

- Don't call `RenderString` with user input — it panics on parse error.
- Don't skip sanitization when rendering untrusted Markdown in browser output.
