<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# yuin/goldmark — v2.1.5 snapshot

Pinned: **v2.1.5**
Source: [tagged source](https://github.com/yuin/goldmark/tree/v2.1.5)

## Basic usage

```go
import (
    "bytes"

    "github.com/yuin/goldmark/v2/extension"
    "github.com/yuin/goldmark/v2/parser"
    "github.com/yuin/goldmark/v2/renderer/html"
)

p := parser.New(
    parser.WithAutoHeadingID(),
    parser.WithExtensions(
        extension.GFMParser,
        extension.FootnoteParser,
        extension.TypographerParser,
    ),
)
r := html.New(
    html.WithHardWraps(),
    html.WithXHTML(),
    html.WithExtensions(
        extension.GFMHTMLRenderer,
        extension.FootnoteHTMLRenderer,
    ),
)

var buf bytes.Buffer
doc := p.Parse(src)
if err := r.Render(&buf, src, doc); err != nil {
    return err
}
htmlOutput := buf.Bytes()
```

## Custom renderer

```go
type myHTMLRendererExtension struct{}

func (e *myHTMLRendererExtension) RendererOptions(_ *html.Config) []html.Option {
    return []html.Option{
        html.WithNodeRenderers(map[ast.NodeKind]html.NodeRenderer{
            KindMyNode: html.NodeRendererFunc(renderMyNode),
        }),
    }
}

r := html.New(html.WithExtensions(&myHTMLRendererExtension{}))
```

## Sanitization

Goldmark does not sanitize HTML output. Keep `html.WithUnsafe` disabled and
sanitize output at the application boundary when rendering untrusted input.

## golusoris usage

- `markdown/` — Goldmark with GFM, footnotes, typographer, automatic heading
  IDs, hard wraps, and XHTML. GFM already includes tables, strikethrough, task
  lists, and linkification; do not register them twice.

## Links

- [Package documentation](https://pkg.go.dev/github.com/yuin/goldmark/v2@v2.1.5)
