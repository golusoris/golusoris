package epub_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/docs/epub"
)

func TestWriteTo(t *testing.T) {
	t.Parallel()
	b := epub.New("Test Book")
	b.SetAuthor("Tester")
	_, err := b.AddSection("<h1>Ch1</h1><p>Hello.</p>", "Chapter 1")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, b.WriteToWriter(&buf))
	// EPUB files start with PK (zip magic bytes)
	require.True(t, bytes.HasPrefix(buf.Bytes(), []byte("PK")))
}

func TestBookMetadata(t *testing.T) {
	t.Parallel()
	b := epub.New("Metadata Test")
	// These are simple setter calls — just verify they don't panic.
	b.SetDescription("A test book.")
	b.SetLang("de")
	b.SetCover("", "") // empty paths are ignored by go-epub

	var buf bytes.Buffer
	require.NoError(t, b.WriteToWriter(&buf))
	require.NotEmpty(t, buf.Bytes())
}

func TestAddSectionWithCSS(t *testing.T) {
	t.Parallel()
	b := epub.New("CSS Test")
	_, err := b.AddSectionWithCSS("<p>Hello</p>", "Ch1", "")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, b.WriteToWriter(&buf))
	require.NotEmpty(t, buf.Bytes())
}

func TestWrite_ToFile(t *testing.T) {
	t.Parallel()
	b := epub.New("File Test")
	_, err := b.AddSection("<p>Hi</p>", "Intro")
	require.NoError(t, err)

	path := t.TempDir() + "/out.epub"
	require.NoError(t, b.Write(path))
}
