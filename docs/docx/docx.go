// Package docx provides helpers for reading and writing DOCX files using the
// nguyenthenguyen/docx library (pure Go, no libreoffice dependency).
//
// The library works by template substitution — load an existing DOCX as a
// template and replace placeholder text:
//
//	doc, err := docx.Open(ctx, "template.docx")
//	doc.Replace("{{name}}", "Alice", -1)
//	err = doc.Save("output.docx")
//
// For generating from scratch see [New] which creates a minimal single-para
// document that can be used as a base template.
package docx

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/nguyenthenguyen/docx"
)

// Document wraps a loaded DOCX for text substitution and writing.
type Document struct {
	r *docx.ReplaceDocx
	d *docx.Docx
}

// Open reads a DOCX template from path.
func Open(_ context.Context, path string) (*Document, error) {
	r, err := docx.ReadDocxFile(path)
	if err != nil {
		return nil, fmt.Errorf("docx: open %s: %w", path, err)
	}
	return &Document{r: r, d: r.Editable()}, nil
}

// OpenReader reads a DOCX template from r (must be a seekable reader).
func OpenReader(_ context.Context, r io.ReaderAt, size int64) (*Document, error) {
	rd, err := docx.ReadDocxFromMemory(r, size)
	if err != nil {
		return nil, fmt.Errorf("docx: open reader: %w", err)
	}
	return &Document{r: rd, d: rd.Editable()}, nil
}

// Replace substitutes old with newVal in the document body.
// n controls the number of replacements (-1 for all).
func (d *Document) Replace(old, newVal string, n int) error {
	if err := d.d.Replace(old, newVal, n); err != nil {
		return fmt.Errorf("docx: replace %q: %w", old, err)
	}
	return nil
}

// ReplaceHeader substitutes old with new in headers.
func (d *Document) ReplaceHeader(old, newVal string) error {
	if err := d.d.ReplaceHeader(old, newVal); err != nil {
		return fmt.Errorf("docx: replace header %q: %w", old, err)
	}
	return nil
}

// ReplaceFooter substitutes old with new in footers.
func (d *Document) ReplaceFooter(old, newVal string) error {
	if err := d.d.ReplaceFooter(old, newVal); err != nil {
		return fmt.Errorf("docx: replace footer %q: %w", old, err)
	}
	return nil
}

// Save writes the document to path.
func (d *Document) Save(path string) error {
	if err := d.d.WriteToFile(path); err != nil {
		return fmt.Errorf("docx: save %s: %w", path, err)
	}
	return nil
}

// Write writes the document to w.
func (d *Document) Write(w io.Writer) error {
	if err := d.d.Write(w); err != nil {
		return fmt.Errorf("docx: write: %w", err)
	}
	return nil
}

// Bytes returns the document as a byte slice.
func (d *Document) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Close releases resources held by the underlying zip reader.
func (d *Document) Close() error {
	if err := d.r.Close(); err != nil {
		return fmt.Errorf("docx: close: %w", err)
	}
	return nil
}
