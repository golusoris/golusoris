// Package epub provides a thin wrapper over [go-epub] for generating
// EPUB 3.0 files (with EPUB 2.0 table-of-contents for compatibility).
//
// Usage:
//
//	b := epub.New("My Book")
//	b.SetAuthor("Alice")
//	b.AddSection("<h1>Chapter 1</h1><p>Hello.</p>", "Chapter 1")
//	err := b.Write("mybook.epub")
package epub

import (
	"fmt"
	"io"
	"os"

	"github.com/bmaupin/go-epub"
)

// Book wraps an epub.Epub to expose a simplified API.
type Book struct {
	e *epub.Epub
}

// New creates a new Book with the given title.
func New(title string) *Book {
	return &Book{e: epub.NewEpub(title)}
}

// SetAuthor sets the book's author metadata.
func (b *Book) SetAuthor(author string) { b.e.SetAuthor(author) }

// SetDescription sets the book's description metadata.
func (b *Book) SetDescription(desc string) { b.e.SetDescription(desc) }

// SetLang sets the book's language code (e.g. "en", "de"). Default: "en".
func (b *Book) SetLang(lang string) { b.e.SetLang(lang) }

// SetCover sets the book cover image from a local path and returns the
// internal image path for use in CSS or content.
func (b *Book) SetCover(imgPath, cssSrc string) {
	b.e.SetCover(imgPath, cssSrc)
}

// AddCSS embeds a CSS file and returns the internal path usable in sections.
func (b *Book) AddCSS(source, internalFilename string) (string, error) {
	path, err := b.e.AddCSS(source, internalFilename)
	if err != nil {
		return "", fmt.Errorf("epub: add css: %w", err)
	}
	return path, nil
}

// AddImage embeds an image file and returns the internal path.
func (b *Book) AddImage(source, internalFilename string) (string, error) {
	path, err := b.e.AddImage(source, internalFilename)
	if err != nil {
		return "", fmt.Errorf("epub: add image: %w", err)
	}
	return path, nil
}

// AddSection adds a content section.
//   - body is the HTML body content.
//   - title appears in the table of contents (pass "" to omit from ToC).
//
// Returns the internal path of the section file.
func (b *Book) AddSection(body, title string) (string, error) {
	path, err := b.e.AddSection(body, title, "", "")
	if err != nil {
		return "", fmt.Errorf("epub: add section: %w", err)
	}
	return path, nil
}

// AddSectionWithCSS adds a section linked to an internal CSS path.
func (b *Book) AddSectionWithCSS(body, title, cssPath string) (string, error) {
	path, err := b.e.AddSection(body, title, "", cssPath)
	if err != nil {
		return "", fmt.Errorf("epub: add section: %w", err)
	}
	return path, nil
}

// Write writes the EPUB to dest path.
func (b *Book) Write(dest string) error {
	if err := b.e.Write(dest); err != nil {
		return fmt.Errorf("epub: write %s: %w", dest, err)
	}
	return nil
}

// WriteToWriter writes the EPUB to w via a temp file (go-epub requires a path).
func (b *Book) WriteToWriter(w io.Writer) error {
	f, err := os.CreateTemp("", "golusoris-epub-*.epub")
	if err != nil {
		return fmt.Errorf("epub: create temp: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(name) }()

	if err := b.e.Write(name); err != nil {
		return fmt.Errorf("epub: write temp: %w", err)
	}
	var data []byte
	data, err = os.ReadFile(name) //nolint:gosec // G304: temp file path from os.CreateTemp, not user input
	if err != nil {
		return fmt.Errorf("epub: read temp: %w", err)
	}
	_, err = w.Write(data)
	return err
}
