# yuin/goldmark — v1.7.12 snapshot

Pinned: **v1.7.12**
Source: https://pkg.go.dev/github.com/yuin/goldmark@v1.7.12

## Basic usage

```go
import "github.com/yuin/goldmark"

md := goldmark.New(
    goldmark.WithExtensions(
        extension.GFM,              // GitHub Flavoured Markdown
        extension.Table,
        extension.Strikethrough,
        extension.TaskList,
        extension.DefinitionList,
        extension.Footnote,
        highlighting.NewHighlighting(  // syntax highlighting
            highlighting.WithStyle("github"),
        ),
    ),
    goldmark.WithRendererOptions(
        html.WithHardWraps(),
        html.WithXHTML(),
        html.WithUnsafe(),   // allow raw HTML in source
    ),
    goldmark.WithParserOptions(
        parser.WithAutoHeadingID(),
    ),
)

var buf bytes.Buffer
if err := md.Convert(src, &buf); err != nil {
    return err
}
htmlOutput := buf.Bytes()
```

## Custom renderer

```go
import "github.com/yuin/goldmark/renderer"

md := goldmark.New(
    goldmark.WithRenderer(
        renderer.NewRenderer(renderer.WithNodeRenderers(
            util.Prioritized(myCustomRenderer, 1000),
        )),
    ),
)
```

## Sanitization

goldmark does not sanitize HTML output. Wrap with a sanitizer (e.g. `microcosm-cc/bluemonday`) when rendering untrusted input.

## golusoris usage

- `markdown/` — goldmark instance with GFM + syntax highlighting provided via fx.

## Links

- Changelog: https://github.com/yuin/goldmark/blob/master/CHANGELOG.md
