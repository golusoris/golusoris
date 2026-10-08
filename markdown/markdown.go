// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package markdown renders Markdown to HTML using goldmark with GitHub
// Flavored Markdown extensions enabled: tables, strikethrough, task lists,
// linkify, and auto-heading IDs.
//
// Usage:
//
//	html, err := markdown.Render([]byte("# Hello\nWorld"))
//	safe, err := markdown.RenderString("**bold**")
package markdown

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
)

var (
	gmParser = parser.New(
		parser.WithAutoHeadingID(),
		parser.WithExtensions(
			extension.GFMParser,
			extension.FootnoteParser,
			extension.TypographerParser,
		),
	)
	gmRenderer = html.New(
		html.WithHardWraps(),
		html.WithXHTML(),
		html.WithExtensions(
			extension.GFMHTMLRenderer,
			extension.FootnoteHTMLRenderer,
		),
	)
)

func renderTo(buf *bytes.Buffer, src []byte) error {
	doc := gmParser.Parse(src)
	if err := gmRenderer.Render(buf, src, doc); err != nil {
		return fmt.Errorf("goldmark render: %w", err)
	}
	return nil
}

// Render converts Markdown src to HTML. The output is not sanitized — callers
// should run output through a sanitizer (e.g. bluemonday) when rendering
// untrusted user content.
func Render(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := renderTo(&buf, src); err != nil {
		return nil, fmt.Errorf("markdown: render: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderString is a convenience wrapper that accepts and returns strings.
func RenderString(src string) (string, error) {
	out, err := Render([]byte(src))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// RenderTo writes the HTML representation of src to buf.
func RenderTo(buf *bytes.Buffer, src []byte) error {
	if buf == nil {
		return errors.New("markdown: convert: nil output buffer")
	}
	if err := renderTo(buf, src); err != nil {
		return fmt.Errorf("markdown: convert: %w", err)
	}
	return nil
}
