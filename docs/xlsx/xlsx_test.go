package xlsx_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/docs/xlsx"
)

func TestWriteRead(t *testing.T) {
	t.Parallel()
	f := xlsx.New()
	defer func() { _ = f.Close() }()

	require.NoError(t, f.SetHeader("Sheet1", []string{"Name", "Score"}))
	require.NoError(t, f.AppendRow("Sheet1", []any{"Alice", 99}))
	require.NoError(t, f.AppendRow("Sheet1", []any{"Bob", 42}))

	var buf bytes.Buffer
	_, err := f.WriteTo(&buf)
	require.NoError(t, err)
	require.NotEmpty(t, buf.Bytes())

	f2, err := xlsx.OpenReader(context.Background(), bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	defer func() { _ = f2.Close() }()

	rows, err := f2.ReadRows("Sheet1")
	require.NoError(t, err)
	require.Len(t, rows, 3) // header + 2 data rows
	require.Equal(t, "Alice", rows[1][0])
	require.Equal(t, "99", rows[1][1])
}

func TestSheets(t *testing.T) {
	t.Parallel()
	f := xlsx.New()
	defer func() { _ = f.Close() }()
	require.NoError(t, f.SetHeader("Data", []string{"A"}))
	require.Contains(t, f.Sheets(), "Data")
}

func TestSetCellGetCell(t *testing.T) {
	t.Parallel()
	f := xlsx.New()
	defer func() { _ = f.Close() }()

	require.NoError(t, f.SetHeader("S", []string{"X"}))
	require.NoError(t, f.SetCell("S", "B2", "hello"))
	v, err := f.GetCell("S", "B2")
	require.NoError(t, err)
	require.Equal(t, "hello", v)
}

func TestSaveAsAndOpen(t *testing.T) {
	t.Parallel()
	f := xlsx.New()
	defer func() { _ = f.Close() }()
	require.NoError(t, f.SetHeader("S", []string{"Col"}))
	require.NoError(t, f.AppendRow("S", []any{"val"}))

	path := t.TempDir() + "/out.xlsx"
	require.NoError(t, f.SaveAs(path))

	f2, err := xlsx.Open(context.Background(), path)
	require.NoError(t, err)
	defer func() { _ = f2.Close() }()
	require.NotNil(t, f2.Raw())
}

func TestReadRowsFunc(t *testing.T) {
	t.Parallel()
	f := xlsx.New()
	defer func() { _ = f.Close() }()
	require.NoError(t, f.SetHeader("S", []string{"A"}))
	require.NoError(t, f.AppendRow("S", []any{"row1"}))

	path := t.TempDir() + "/out.xlsx"
	require.NoError(t, f.SaveAs(path))

	rows, err := xlsx.ReadRows(context.Background(), path, "S")
	require.NoError(t, err)
	require.Len(t, rows, 2)
}
