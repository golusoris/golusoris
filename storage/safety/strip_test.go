package safety_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"testing"

	"github.com/golusoris/golusoris/storage/safety"
)

func newTestStripper(t *testing.T, opts safety.StripOptions) safety.Stripper {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	return safety.NewStripperForTest(opts, logger)
}

func defaultStripOpts() safety.StripOptions {
	return safety.StripOptions{AutoOrient: true, JPEGQuality: 85, MaxPixels: 40_000_000}
}

func TestStrip_DropsJPEGEXIF(t *testing.T) {
	t.Parallel()
	src := jpegWithEXIF(t, 16, 16, 1)
	if !bytes.Contains(src, []byte("Exif\x00\x00")) {
		t.Fatal("fixture should contain an EXIF marker before stripping")
	}

	s := newTestStripper(t, defaultStripOpts())
	out, ctype, err := s.Strip(context.Background(), bytes.NewReader(src), "image/jpeg")
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if ctype != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", ctype)
	}
	got, err := io.ReadAll(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if bytes.Contains(got, []byte("Exif\x00\x00")) {
		t.Fatal("output still contains EXIF marker after Strip")
	}
	if _, _, err = image.Decode(bytes.NewReader(got)); err != nil {
		t.Fatalf("stripped output not a valid image: %v", err)
	}
}

func TestStrip_DropsPNGText(t *testing.T) {
	t.Parallel()
	src := pngWithText(t, 8, 8)
	if !bytes.Contains(src, []byte("Secret-GPS")) {
		t.Fatal("fixture should contain a tEXt keyword before stripping")
	}

	s := newTestStripper(t, defaultStripOpts())
	out, ctype, err := s.Strip(context.Background(), bytes.NewReader(src), "image/png")
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if ctype != "image/png" {
		t.Fatalf("content type = %q, want image/png", ctype)
	}
	got, err := io.ReadAll(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if bytes.Contains(got, []byte("Secret-GPS")) {
		t.Fatal("output still contains tEXt chunk after Strip")
	}
}

func TestStrip_AutoOrientRotates(t *testing.T) {
	t.Parallel()
	// 20x10 image, Orientation=6 (rotate 90° CW) => output should be 10x20.
	src := jpegWithEXIF(t, 20, 10, 6)
	s := newTestStripper(t, defaultStripOpts())
	out, _, err := s.Strip(context.Background(), bytes.NewReader(src), "image/jpeg")
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	cfg, _, err := image.DecodeConfig(out)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Width != 10 || cfg.Height != 20 {
		t.Fatalf("auto-orient dims = %dx%d, want 10x20", cfg.Width, cfg.Height)
	}
}

func TestStrip_AutoOrientDisabledKeepsDims(t *testing.T) {
	t.Parallel()
	src := jpegWithEXIF(t, 20, 10, 6)
	opts := defaultStripOpts()
	opts.AutoOrient = false
	s := newTestStripper(t, opts)
	out, _, err := s.Strip(context.Background(), bytes.NewReader(src), "image/jpeg")
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	cfg, _, err := image.DecodeConfig(out)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Width != 20 || cfg.Height != 10 {
		t.Fatalf("dims = %dx%d, want 20x10 (no rotation)", cfg.Width, cfg.Height)
	}
}

func TestStrip_DecodeBomb(t *testing.T) {
	t.Parallel()
	opts := defaultStripOpts()
	opts.MaxPixels = 100 // tiny cap
	s := newTestStripper(t, opts)
	src := jpegWithEXIF(t, 64, 64, 1) // 4096 px > 100
	_, _, err := s.Strip(context.Background(), bytes.NewReader(src), "image/jpeg")
	if !errors.Is(err, safety.ErrImageTooLarge) {
		t.Fatalf("decode bomb: want ErrImageTooLarge, got %v", err)
	}
}

func TestStrip_UnsupportedType(t *testing.T) {
	t.Parallel()
	s := newTestStripper(t, defaultStripOpts())
	for _, ct := range []string{"image/svg+xml", "application/pdf", "text/html", ""} {
		_, _, err := s.Strip(context.Background(), bytes.NewReader([]byte("x")), ct)
		if !errors.Is(err, safety.ErrUnsupportedType) {
			t.Fatalf("type %q: want ErrUnsupportedType, got %v", ct, err)
		}
	}
}

func TestStrip_GIFRoundTrips(t *testing.T) {
	t.Parallel()
	s := newTestStripper(t, defaultStripOpts())
	src := gifBytes(t, 8, 8)
	out, ctype, err := s.Strip(context.Background(), bytes.NewReader(src), "image/gif")
	if err != nil {
		t.Fatalf("Strip gif: %v", err)
	}
	if ctype != "image/gif" {
		t.Fatalf("ctype = %q, want image/gif", ctype)
	}
	if _, _, err = image.Decode(out); err != nil {
		t.Fatalf("gif output invalid: %v", err)
	}
}

// TestStrip_AutoOrientNoEXIF exercises the orientation-read-failure path: a
// plain JPEG (no APP1) with auto_orient on must strip cleanly, dims unchanged.
func TestStrip_AutoOrientNoEXIF(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solidImage(12, 8), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	s := newTestStripper(t, defaultStripOpts())
	out, _, err := s.Strip(context.Background(), bytes.NewReader(buf.Bytes()), "image/jpeg")
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	cfg, _, err := image.DecodeConfig(out)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Width != 12 || cfg.Height != 8 {
		t.Fatalf("dims = %dx%d, want 12x8", cfg.Width, cfg.Height)
	}
}

func TestStrip_JPGAlias(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, solidImage(4, 4), nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	s := newTestStripper(t, defaultStripOpts())
	_, ctype, err := s.Strip(context.Background(), bytes.NewReader(buf.Bytes()), "image/jpg")
	if err != nil {
		t.Fatalf("Strip image/jpg: %v", err)
	}
	if ctype != "image/jpeg" {
		t.Fatalf("ctype = %q, want image/jpeg", ctype)
	}
}

func TestStrip_CorruptInput(t *testing.T) {
	t.Parallel()
	s := newTestStripper(t, defaultStripOpts())
	// Valid PNG signature so DecodeConfig is reached, then garbage.
	bad := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)
	if _, _, err := s.Strip(context.Background(), bytes.NewReader(bad), "image/png"); err == nil {
		t.Fatal("corrupt input: want error, got nil")
	}
}

// --- fixtures ---

func solidImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xFF})
		}
	}
	return img
}

// jpegWithEXIF encodes a JPEG then splices in an APP1 EXIF segment carrying the
// given Orientation so tests can exercise the strip + auto-orient paths.
func jpegWithEXIF(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	var base bytes.Buffer
	if err := jpeg.Encode(&base, solidImage(w, h), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	app1 := exifAPP1(orientation)
	raw := base.Bytes()
	// Insert APP1 right after SOI (first two bytes 0xFFD8).
	out := make([]byte, 0, len(raw)+len(app1))
	out = append(out, raw[:2]...)
	out = append(out, app1...)
	out = append(out, raw[2:]...)
	return out
}

// exifAPP1 builds a minimal APP1 segment: "Exif\0\0" + TIFF header + one IFD0
// entry (Orientation, SHORT).
func exifAPP1(orientation int) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")                                    // big-endian
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x002A)) // magic 42
	_ = binary.Write(&tiff, binary.BigEndian, uint32(8))      // IFD0 offset
	_ = binary.Write(&tiff, binary.BigEndian, uint16(1))      // one entry
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x0112)) // tag Orientation
	_ = binary.Write(&tiff, binary.BigEndian, uint16(3))      // type SHORT
	_ = binary.Write(&tiff, binary.BigEndian, uint32(1))      // count
	_ = binary.Write(&tiff, binary.BigEndian, uint16(orientation))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0)) // pad value field
	_ = binary.Write(&tiff, binary.BigEndian, uint32(0)) // next IFD = 0

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segLen := len(payload) + 2
	var seg bytes.Buffer
	seg.Write([]byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)})
	seg.Write(payload)
	return seg.Bytes()
}

func pngWithText(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, solidImage(w, h)); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return spliceTEXt(buf.Bytes(), "Secret-GPS", "48.2N,16.4E")
}

// spliceTEXt inserts a tEXt chunk after the IHDR chunk of a PNG.
func spliceTEXt(pngBytes []byte, key, val string) []byte {
	const sigLen = 8
	ihdrEnd := sigLen + 4 + 4 + 13 + 4 // len+type+data+crc
	data := append([]byte(key), 0)
	data = append(data, []byte(val)...)
	chunk := makeChunk("tEXt", data)
	out := make([]byte, 0, len(pngBytes)+len(chunk))
	out = append(out, pngBytes[:ihdrEnd]...)
	out = append(out, chunk...)
	out = append(out, pngBytes[ihdrEnd:]...)
	return out
}

func makeChunk(ctype string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(ctype)
	b.Write(data)
	crc := crc32.ChecksumIEEE(append([]byte(ctype), data...))
	_ = binary.Write(&b, binary.BigEndian, crc)
	return b.Bytes()
}

func gifBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	pal := color.Palette{color.Black, color.White}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}
