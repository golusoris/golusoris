// Package markdown renders Markdown to HTML using goldmark with GitHub
// Flavored Markdown extensions enabled: tables, strikethrough, task lists,
// linkify, and auto-heading IDs.
//
// Usage:
//
//	html, err := markdown.Render([]byte("# Hello\nWorld"))
//	safe := markdown.RenderString("**bold**")
package markdown

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

var gm = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,        // tables, strikethrough, task lists, linkify
		extension.Footnote,   // [^1] footnotes
		extension.Typographer, // smart quotes, dashes
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(), // <h2 id="hello">
	),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		html.WithXHTML(),
	),
)

// Render converts Markdown src to HTML. The output is not sanitized — callers
// should run output through a sanitizer (e.g. bluemonday) when rendering
// untrusted user content.
func Render(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := gm.Convert(src, &buf); err != nil {
		return nil, fmt.Errorf("markdown: render: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderString is a convenience wrapper that accepts and returns strings.
// Panics on error (suitable for static/trusted content at init time).
func RenderString(src string) string {
	out, err := Render([]byte(src))
	if err != nil {
		panic(fmt.Sprintf("markdown: %v", err))
	}
	return string(out)
}

// RenderTo writes the HTML representation of src to buf.
func RenderTo(buf *bytes.Buffer, src []byte) error {
	return gm.Convert(src, buf)
}
