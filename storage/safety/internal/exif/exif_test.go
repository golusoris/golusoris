package exif_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/golusoris/golusoris/storage/safety/internal/exif"
)

func exifJPEG(orientation int) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x002A))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(8))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(1))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(3))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(1))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(orientation))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(0))

	return wrapAPP1(tiff.Bytes())
}

// wrapAPP1 wraps a TIFF block in an "Exif\0\0" APP1 segment after a JPEG SOI.
func wrapAPP1(tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segLen := len(payload) + 2
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8, 0xFF, 0xE1, byte(segLen >> 8), byte(segLen)})
	out.Write(payload)
	return out.Bytes()
}

func TestOrientation(t *testing.T) {
	t.Parallel()
	for o := 1; o <= 8; o++ {
		got, err := exif.Orientation(exifJPEG(o))
		if err != nil {
			t.Fatalf("orientation %d: %v", o, err)
		}
		if got != o {
			t.Fatalf("orientation = %d, want %d", got, o)
		}
	}
}

func TestOrientation_NoTag(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"not jpeg":        []byte("not an image"),
		"empty":           {},
		"soi only":        {0xFF, 0xD8},
		"truncated app1":  {0xFF, 0xD8, 0xFF, 0xE1, 0x00},
		"bad segment len": {0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x01},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := exif.Orientation(in); !errors.Is(err, exif.ErrNoOrientation) {
				t.Fatalf("%s: err = %v; want ErrNoOrientation", name, err)
			}
		})
	}
}

func TestOrientation_LittleEndian(t *testing.T) {
	t.Parallel()
	var tiff bytes.Buffer
	tiff.WriteString("II")
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x002A))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(1))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(3))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(1))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(6))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0))

	got, err := exif.Orientation(wrapAPP1(tiff.Bytes()))
	if err != nil {
		t.Fatalf("little-endian: %v", err)
	}
	if got != 6 {
		t.Fatalf("little-endian orientation = %d, want 6", got)
	}
}

func TestOrientation_BadByteOrder(t *testing.T) {
	t.Parallel()
	jpg := wrapAPP1([]byte("XX\x00\x2A\x00\x00\x00\x08"))
	if _, err := exif.Orientation(jpg); !errors.Is(err, exif.ErrNoOrientation) {
		t.Fatalf("bad byte order: err = %v; want ErrNoOrientation", err)
	}
}

func TestApply_NonRGBASource(t *testing.T) {
	t.Parallel()
	// image.Gray exercises the imageToRGBA conversion branch.
	src := image.NewGray(image.Rect(0, 0, 3, 2))
	got := exif.Apply(src, 6)
	if got.Bounds().Dx() != 2 || got.Bounds().Dy() != 3 {
		t.Fatalf("gray rotate90 dims = %v, want 2x3", got.Bounds())
	}
}

func TestOrientation_OutOfRange(t *testing.T) {
	t.Parallel()
	if _, err := exif.Orientation(exifJPEG(99)); !errors.Is(err, exif.ErrNoOrientation) {
		t.Fatalf("orientation 99: err = %v; want ErrNoOrientation", err)
	}
}

func TestApply_Dimensions(t *testing.T) {
	t.Parallel()
	src := image.NewRGBA(image.Rect(0, 0, 4, 2)) // 4 wide, 2 tall
	tests := []struct {
		orientation int
		wantW       int
		wantH       int
	}{
		{1, 4, 2},
		{2, 4, 2},
		{3, 4, 2},
		{4, 4, 2},
		{5, 2, 4},
		{6, 2, 4},
		{7, 2, 4},
		{8, 2, 4},
		{0, 4, 2}, // unrecognized => unchanged
	}
	for _, tt := range tests {
		got := exif.Apply(src, tt.orientation)
		b := got.Bounds()
		if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
			t.Fatalf("orientation %d: dims = %dx%d, want %dx%d",
				tt.orientation, b.Dx(), b.Dy(), tt.wantW, tt.wantH)
		}
	}
}

func TestApply_PixelTransform(t *testing.T) {
	t.Parallel()
	// 2x1: left pixel red, right pixel blue. Rotate180 swaps them.
	red := color.RGBA{R: 0xFF, A: 0xFF}
	blue := color.RGBA{B: 0xFF, A: 0xFF}
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, red)
	src.Set(1, 0, blue)

	got := exif.Apply(src, 3) // 180°
	// After 180°, destination (0,0) holds the original right (blue) pixel.
	r, _, b, _ := got.At(0, 0).RGBA()
	if r>>8 != 0 || b>>8 != 0xFF {
		t.Fatalf("rotate180 (0,0) = r%d b%d, want blue (r0 b255)", r>>8, b>>8)
	}
}
