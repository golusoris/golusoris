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
