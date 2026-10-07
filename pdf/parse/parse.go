// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

const maxMergeInputs = 256

var errContextRequired = errors.New("pdf/parse: context is required")

type guardedReadSeeker struct {
	check  func() error
	source io.ReadSeeker
}

func (r guardedReadSeeker) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	n, err := r.source.Read(p)
	if err == nil {
		err = r.check()
	}
	return n, err
}

func (r guardedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	position, err := r.source.Seek(offset, whence)
	if err == nil {
		err = r.check()
	}
	return position, err
}

type guardedWriter struct {
	check  func() error
	target io.Writer
}

func (w guardedWriter) Write(p []byte) (int, error) {
	if err := w.check(); err != nil {
		return 0, err
	}
	n, err := w.target.Write(p)
	if err == nil {
		err = w.check()
	}
	return n, err
}

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
func Info(ctx context.Context, r io.ReadSeeker, fileName string) (Metadata, error) {
	if err := requireContext(ctx); err != nil {
		return Metadata{}, err
	}
	info, err := api.PDFInfo(
		guardedReadSeeker{check: ctx.Err, source: r}, fileName, nil, false,
		model.NewDefaultConfiguration(),
	)
	if err != nil {
		return Metadata{}, fmt.Errorf("pdf/parse: info: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return Metadata{}, fmt.Errorf("pdf/parse: info: %w", err)
	}
	return metaFromInfo(info), nil
}

// InfoFile returns metadata for the PDF at path. path is a caller-selected
// local-file trust boundary; callers must scope untrusted names before use.
func InfoFile(ctx context.Context, path string) (meta Metadata, err error) {
	if err = requireContext(ctx); err != nil {
		return Metadata{}, err
	}
	f, err := os.Open(path) // #nosec G304 -- arbitrary local paths are this API's documented input.
	if err != nil {
		return Metadata{}, fmt.Errorf("pdf/parse: open %s: %w", path, err)
	}
	defer func() {
		// A close failure only surfaces when the read itself succeeded.
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("pdf/parse: close %s: %w", path, cerr)
		}
	}()

	return Info(ctx, f, path)
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
func ParseTime(pdfDate string) time.Time { //nolint:revive // parse.Time would read as a type; the verb form is the documented API
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
func Validate(ctx context.Context, r io.ReadSeeker) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if err := api.Validate(
		guardedReadSeeker{check: ctx.Err, source: r}, model.NewDefaultConfiguration(),
	); err != nil {
		return fmt.Errorf("pdf/parse: validate: %w", err)
	}
	return ctx.Err()
}

// ValidateFile reports whether the PDF at path conforms to the PDF spec.
func ValidateFile(ctx context.Context, path string) (err error) {
	if err = requireContext(ctx); err != nil {
		return err
	}
	f, err := os.Open(path) // #nosec G304 -- arbitrary local paths are this API's documented input.
	if err != nil {
		return fmt.Errorf("pdf/parse: open %s: %w", path, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("pdf/parse: close %s: %w", path, closeErr))
		}
	}()
	return Validate(ctx, f)
}

// Merge merges the PDFs at inFiles into outFile.
func Merge(ctx context.Context, inFiles []string, outFile string) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if len(inFiles) == 0 {
		return nil
	}
	if len(inFiles) > maxMergeInputs {
		return fmt.Errorf("pdf/parse: merge: %d inputs exceed limit %d", len(inFiles), maxMergeInputs)
	}
	if err := writeAtomic(ctx, outFile, func(w io.Writer) (err error) {
		readers, files, openErr := openInputs(ctx, inFiles)
		if openErr != nil {
			return openErr
		}
		defer func() { err = errors.Join(err, closeFiles(files)) }()
		return api.MergeRaw(readers, w, false, model.NewDefaultConfiguration())
	}); err != nil {
		return fmt.Errorf("pdf/parse: merge: %w", err)
	}
	return nil
}

// Optimize streams src through pdfcpu's staged file optimizer and writes dst
// with owner-only permissions. Paths are caller-selected local-file trust
// boundaries; callers must scope untrusted names before use.
func Optimize(ctx context.Context, src, dst string) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if err := writeAtomic(ctx, dst, func(w io.Writer) (err error) {
		f, openErr := os.Open(src) // #nosec G304 -- arbitrary local paths are this API's documented input.
		if openErr != nil {
			return fmt.Errorf("open %s: %w", src, openErr)
		}
		defer func() { err = errors.Join(err, f.Close()) }()
		return api.Optimize(
			guardedReadSeeker{check: ctx.Err, source: f}, w,
			model.NewDefaultConfiguration(),
		)
	}); err != nil {
		return fmt.Errorf("pdf/parse: optimize: %w", err)
	}
	return nil
}

func requireContext(ctx context.Context) error {
	if ctx == nil {
		return errContextRequired
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pdf/parse: context: %w", err)
	}
	return nil
}

func openInputs(ctx context.Context, paths []string) ([]io.ReadSeeker, []*os.File, error) {
	readers := make([]io.ReadSeeker, 0, len(paths))
	files := make([]*os.File, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, errors.Join(err, closeFiles(files))
		}
		f, err := os.Open(path) // #nosec G304 -- arbitrary local paths are this API's documented input.
		if err != nil {
			return nil, nil, errors.Join(fmt.Errorf("open %s: %w", path, err), closeFiles(files))
		}
		files = append(files, f)
		readers = append(readers, guardedReadSeeker{check: ctx.Err, source: f})
	}
	return readers, files, nil
}

func closeFiles(files []*os.File) error {
	var errs []error
	for _, f := range files {
		if err := f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type stagedOutput struct {
	file *os.File
	path string
}

func newStagedOutput(dst string) (*stagedOutput, error) {
	if dst == "" {
		return nil, errors.New("output path is required")
	}
	temp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create staged output: %w", err)
	}
	return &stagedOutput{file: temp, path: temp.Name()}, nil
}

func (s *stagedOutput) cleanup() error {
	var errs []error
	if s.file != nil {
		errs = append(errs, s.file.Close())
		s.file = nil
	}
	if s.path != "" {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		s.path = ""
	}
	return errors.Join(errs...)
}

func (s *stagedOutput) commit(ctx context.Context, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync staged output: %w", err)
	}
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close staged output: %w", err)
	}
	s.file = nil
	if err := os.Rename(s.path, dst); err != nil {
		return fmt.Errorf("commit staged output: %w", err)
	}
	s.path = ""
	return nil
}

func writeAtomic(ctx context.Context, dst string, write func(io.Writer) error) (err error) {
	staged, err := newStagedOutput(dst)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, staged.cleanup()) }()
	if err = write(guardedWriter{check: ctx.Err, target: staged.file}); err != nil {
		return err
	}
	return staged.commit(ctx, dst)
}
