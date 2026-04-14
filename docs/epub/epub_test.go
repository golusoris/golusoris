package epub_test

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestAddCSS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cssFile := filepath.Join(dir, "style.css")
	require.NoError(t, os.WriteFile(cssFile, []byte("body { color: red; }"), 0o640))

	b := epub.New("CSS Book")
	internalPath, err := b.AddCSS(cssFile, "style.css")
	require.NoError(t, err)
	require.NotEmpty(t, internalPath)
}

func TestAddImage(t *testing.T) {
	t.Parallel()
	// Minimal 1×1 GIF so go-epub accepts it as a valid image.
	gifData := []byte{
		0x47, 0x49, 0x46, 0x38, 0x39, 0x61, // GIF89a
		0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, // 1x1, no CT
		0x21, 0xf9, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, // GCE
		0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, // image desc
		0x02, 0x02, 0x44, 0x01, 0x00, // image data
		0x3b, // trailer
	}
	dir := t.TempDir()
	imgFile := filepath.Join(dir, "pixel.gif")
	require.NoError(t, os.WriteFile(imgFile, gifData, 0o640))

	b := epub.New("Image Book")
	internalPath, err := b.AddImage(imgFile, "pixel.gif")
	require.NoError(t, err)
	require.NotEmpty(t, internalPath)
}
