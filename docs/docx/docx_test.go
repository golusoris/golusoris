// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docx_test

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/docs/docx"
)

func createTestDOCX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	files := map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>Hello {{name}}!</w:t></w:r></w:p></w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`,
		"word/header1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:p><w:r><w:t>Header {{header_val}}</w:t></w:r></w:p></w:hdr>`,
		"word/footer1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:p><w:r><w:t>Footer {{footer_val}}</w:t></w:r></w:p></w:ftr>`,
	}

	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(w, content)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func readZipFile(t *testing.T, zipBytes []byte, filename string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, err)
	for _, f := range zr.File {
		if f.Name == filename {
			rc, err := f.Open()
			require.NoError(t, err)
			defer rc.Close()
			content, err := io.ReadAll(rc)
			require.NoError(t, err)
			return string(content)
		}
	}
	t.Fatalf("file %q not found in zip", filename)
	return ""
}

func TestOpenReader(t *testing.T) {
	t.Parallel()

	t.Run("positive open valid memory reader", func(t *testing.T) {
		t.Parallel()
		data := createTestDOCX(t)
		doc, err := docx.OpenReader(t.Context(), bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)
		require.NotNil(t, doc)
		require.NoError(t, doc.Close())
	})

	t.Run("negative corrupt archive", func(t *testing.T) {
		t.Parallel()
		corrupt := []byte("corrupt-data-not-a-zip")
		_, err := docx.OpenReader(t.Context(), bytes.NewReader(corrupt), int64(len(corrupt)))
		require.Error(t, err)
		require.ErrorContains(t, err, "docx: open reader")
	})

	t.Run("boundary zero size", func(t *testing.T) {
		t.Parallel()
		_, err := docx.OpenReader(t.Context(), bytes.NewReader(nil), 0)
		require.Error(t, err)
		require.ErrorContains(t, err, "docx: open reader")
	})
}

func TestOpen(t *testing.T) {
	t.Parallel()

	t.Run("positive open from file", func(t *testing.T) {
		t.Parallel()
		data := createTestDOCX(t)
		path := filepath.Join(t.TempDir(), "sample.docx")
		require.NoError(t, os.WriteFile(path, data, 0o600))

		doc, err := docx.Open(t.Context(), path)
		require.NoError(t, err)
		require.NotNil(t, doc)
		require.NoError(t, doc.Close())
	})

	t.Run("negative non-existent file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "missing.docx")
		_, err := docx.Open(t.Context(), path)
		require.Error(t, err)
		require.ErrorContains(t, err, "docx: open")
	})
}

func TestReplace(t *testing.T) {
	t.Parallel()

	data := createTestDOCX(t)
	doc, err := docx.OpenReader(t.Context(), bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	defer doc.Close()

	require.NoError(t, doc.Replace("{{name}}", "Gopher", -1))
	require.NoError(t, doc.Replace("non_existent", "value", 1))

	b, err := doc.Bytes()
	require.NoError(t, err)
	content := readZipFile(t, b, "word/document.xml")
	require.True(t, strings.Contains(content, "Hello Gopher!"))
}

func TestReplaceHeaderAndFooter(t *testing.T) {
	t.Parallel()

	data := createTestDOCX(t)
	doc, err := docx.OpenReader(t.Context(), bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	defer doc.Close()

	require.NoError(t, doc.ReplaceHeader("{{header_val}}", "TitleHeader"))
	require.NoError(t, doc.ReplaceFooter("{{footer_val}}", "PageFooter"))

	b, err := doc.Bytes()
	require.NoError(t, err)
	header := readZipFile(t, b, "word/header1.xml")
	require.True(t, strings.Contains(header, "Header TitleHeader"))
	footer := readZipFile(t, b, "word/footer1.xml")
	require.True(t, strings.Contains(footer, "Footer PageFooter"))
}

func TestSaveAndWrite(t *testing.T) {
	t.Parallel()

	data := createTestDOCX(t)
	doc, err := docx.OpenReader(t.Context(), bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	defer doc.Close()

	var buf bytes.Buffer
	require.NoError(t, doc.Write(&buf))
	require.Positive(t, buf.Len())
	require.True(t, bytes.HasPrefix(buf.Bytes(), []byte("PK")))

	outPath := filepath.Join(t.TempDir(), "output.docx")
	require.NoError(t, doc.Save(outPath))

	stat, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Positive(t, stat.Size())

	badPath := filepath.Join(t.TempDir(), "nonexistent", "dir", "output.docx")
	require.Error(t, doc.Save(badPath))
}
