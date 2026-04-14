// Package parse extracts text, metadata, and page information from PDF files
// using pdfcpu (pure-Go, no CGO required).
//
// Usage:
//
//	info, err := parse.Info(ctx, r, "report.pdf")
//	// info.PageCount, info.Title, info.Author, ...
//
//	err = parse.Validate(ctx, r)
//	err = parse.Merge(ctx, []string{"a.pdf", "b.pdf"}, "out.pdf")
package parse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// Metadata holds document-level PDF metadata extracted from the info dict.
type Metadata struct {
	Title        string
	Author       string
	Subject      string
	Keywords     []string
	Creator      string
	Producer     string
	CreationDate string
	ModifiedDate string
	Pages        int
	Version      string
	Tagged       bool
	Linearized   bool
	HasForm      bool
}

// Info returns metadata for the PDF read from r. fileName is used only for
// error messages; pass "" or the source path.
func Info(_ context.Context, r io.ReadSeeker, fileName string) (Metadata, error) {
	info, err := api.PDFInfo(r, fileName, nil, false, model.NewDefaultConfiguration())
	if err != nil {
		return Metadata{}, fmt.Errorf("pdf/parse: info: %w", err)
	}
	return metaFromInfo(info), nil
}

// InfoFile returns metadata for the PDF at path.
func InfoFile(_ context.Context, path string) (Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("pdf/parse: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := api.PDFInfo(f, path, nil, false, model.NewDefaultConfiguration())
	if err != nil {
		return Metadata{}, fmt.Errorf("pdf/parse: info %s: %w", path, err)
	}
	return metaFromInfo(info), nil
}

func metaFromInfo(i *pdfcpu.PDFInfo) Metadata {
	return Metadata{
		Title:        i.Title,
		Author:       i.Author,
		Subject:      i.Subject,
		Keywords:     i.Keywords,
		Creator:      i.Creator,
		Producer:     i.Producer,
		CreationDate: i.CreationDate,
		ModifiedDate: i.ModificationDate,
		Pages:        i.PageCount,
		Version:      i.Version,
		Tagged:       i.Tagged,
		Linearized:   i.Linearized,
		HasForm:      i.Form,
	}
}

// ParseTime attempts to parse a PDF date string (D:YYYYMMDDHHmmSS).
// Returns zero time on failure.
func ParseTime(pdfDate string) time.Time {
	s := strings.TrimPrefix(pdfDate, "D:")
	if len(s) < 8 {
		return time.Time{}
	}
	// Try formats from most to least precise; guard length before slicing.
	for _, layout := range []string{"20060102150405", "200601021504", "20060102"} {
		if len(s) < len(layout) {
			continue
		}
		if t, err := time.Parse(layout, s[:len(layout)]); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Validate reports whether the PDF read from r conforms to the PDF spec.
func Validate(_ context.Context, r io.ReadSeeker) error {
	if err := api.Validate(r, model.NewDefaultConfiguration()); err != nil {
		return fmt.Errorf("pdf/parse: validate: %w", err)
	}
	return nil
}

// ValidateFile reports whether the PDF at path conforms to the PDF spec.
func ValidateFile(_ context.Context, path string) error {
	if err := api.ValidateFile(path, model.NewDefaultConfiguration()); err != nil {
		return fmt.Errorf("pdf/parse: validate %s: %w", path, err)
	}
	return nil
}

// Merge merges the PDFs at inFiles into outFile.
func Merge(_ context.Context, inFiles []string, outFile string) error {
	if len(inFiles) == 0 {
		return nil
	}
	if err := api.MergeCreateFile(inFiles, outFile, false, model.NewDefaultConfiguration()); err != nil {
		return fmt.Errorf("pdf/parse: merge: %w", err)
	}
	return nil
}

// Optimize reads src, reduces redundant objects, and writes the result to dst.
func Optimize(_ context.Context, src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("pdf/parse: read %s: %w", src, err)
	}
	var buf bytes.Buffer
	if err := api.Optimize(bytes.NewReader(data), &buf, model.NewDefaultConfiguration()); err != nil {
		return fmt.Errorf("pdf/parse: optimize: %w", err)
	}
	if err := os.WriteFile(dst, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("pdf/parse: write %s: %w", dst, err)
	}
	return nil
}
