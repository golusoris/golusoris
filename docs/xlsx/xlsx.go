// Package xlsx provides thin helpers over [excelize] for reading and writing
// XLSX spreadsheets.
//
// Usage (write):
//
//	f, err := xlsx.New()
//	f.SetHeader("Sheet1", []string{"Name", "Score"})
//	f.AppendRow("Sheet1", []any{"Alice", 99})
//	err = f.SaveAs("report.xlsx")
//
// Usage (read):
//
//	rows, err := xlsx.ReadRows(ctx, "report.xlsx", "Sheet1")
//	for _, row := range rows {
//	    fmt.Println(row)
//	}
package xlsx

import (
	"context"
	"fmt"
	"io"

	"github.com/xuri/excelize/v2"
)

// File wraps an excelize.File with convenience methods.
type File struct {
	f *excelize.File
}

// New creates an in-memory XLSX workbook.
func New() *File {
	return &File{f: excelize.NewFile()}
}

// Open reads an XLSX file from disk.
func Open(_ context.Context, path string) (*File, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("xlsx: open %s: %w", path, err)
	}
	return &File{f: f}, nil
}

// OpenReader reads an XLSX workbook from r.
func OpenReader(_ context.Context, r io.Reader) (*File, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("xlsx: open reader: %w", err)
	}
	return &File{f: f}, nil
}

// Close releases resources.
func (f *File) Close() error {
	return f.f.Close()
}

// SetHeader writes a header row (row 1) on sheet.
func (f *File) SetHeader(sheet string, cols []string) error {
	if _, err := f.f.NewSheet(sheet); err != nil {
		return fmt.Errorf("xlsx: new sheet %s: %w", sheet, err)
	}
	for i, col := range cols {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return fmt.Errorf("xlsx: header cell: %w", err)
		}
		if err := f.f.SetCellValue(sheet, cell, col); err != nil {
			return fmt.Errorf("xlsx: set header %s: %w", cell, err)
		}
	}
	return nil
}

// AppendRow appends values at the next available row on sheet.
func (f *File) AppendRow(sheet string, vals []any) error {
	rows, err := f.f.GetRows(sheet)
	if err != nil {
		return fmt.Errorf("xlsx: get rows: %w", err)
	}
	rowIdx := len(rows) + 1
	for i, v := range vals {
		cell, err := excelize.CoordinatesToCellName(i+1, rowIdx)
		if err != nil {
			return fmt.Errorf("xlsx: cell name: %w", err)
		}
		if err := f.f.SetCellValue(sheet, cell, v); err != nil {
			return fmt.Errorf("xlsx: set cell %s: %w", cell, err)
		}
	}
	return nil
}

// SetCell writes a value to a named cell (e.g. "A1").
func (f *File) SetCell(sheet, cell string, val any) error {
	if err := f.f.SetCellValue(sheet, cell, val); err != nil {
		return fmt.Errorf("xlsx: set cell %s!%s: %w", sheet, cell, err)
	}
	return nil
}

// GetCell reads the value of cell as a string.
func (f *File) GetCell(sheet, cell string) (string, error) {
	v, err := f.f.GetCellValue(sheet, cell)
	if err != nil {
		return "", fmt.Errorf("xlsx: get cell %s!%s: %w", sheet, cell, err)
	}
	return v, nil
}

// Sheets returns the sheet names in the workbook.
func (f *File) Sheets() []string { return f.f.GetSheetList() }

// ReadRows returns all rows from sheet as string slices.
func (f *File) ReadRows(sheet string) ([][]string, error) {
	rows, err := f.f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx: get rows %s: %w", sheet, err)
	}
	return rows, nil
}

// SaveAs writes the workbook to path.
func (f *File) SaveAs(path string) error {
	if err := f.f.SaveAs(path); err != nil {
		return fmt.Errorf("xlsx: save %s: %w", path, err)
	}
	return nil
}

// WriteTo writes the workbook to w without touching the filesystem.
func (f *File) WriteTo(w io.Writer) (int64, error) {
	n, err := f.f.WriteTo(w)
	if err != nil {
		return n, fmt.Errorf("xlsx: write: %w", err)
	}
	return n, nil
}

// Raw exposes the underlying *excelize.File for advanced use.
func (f *File) Raw() *excelize.File { return f.f }

// ReadRows is a convenience function that opens path and returns all rows
// from sheet without keeping the file handle open.
func ReadRows(_ context.Context, path, sheet string) ([][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("xlsx: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("xlsx: get rows %s: %w", sheet, err)
	}
	return rows, nil
}
