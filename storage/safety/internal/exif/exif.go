// Package exif reads only the JPEG EXIF Orientation tag and bakes the
// corresponding rotation/flip into pixels. It is deliberately minimal: a full
// EXIF parser is out of scope, since the surrounding safety package strips all
// metadata by re-encoding. No third-party dependency.
package exif

import (
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
)

// ErrNoOrientation indicates no usable EXIF Orientation tag was found.
var ErrNoOrientation = errors.New("exif: no orientation tag")

const (
	markerSOI  = 0xD8
	markerAPP1 = 0xE1
	markerSOS  = 0xDA
	tagOrient  = 0x0112
)

// Orientation extracts the EXIF Orientation value (1-8) from a JPEG byte slice.
// It returns ErrNoOrientation when the tag is absent or unreadable.
func Orientation(jpegBytes []byte) (int, error) {
	app1, err := findAPP1(jpegBytes)
	if err != nil {
		return 0, err
	}
	return parseOrientation(app1)
}

// findAPP1 returns the payload of the first APP1 segment carrying an "Exif\0\0"
// header, scanning JPEG marker segments without decoding pixel data.
func findAPP1(b []byte) ([]byte, error) {
	if len(b) < 2 || b[0] != 0xFF || b[1] != markerSOI {
		return nil, ErrNoOrientation
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return nil, ErrNoOrientation
		}
		marker := b[i+1]
		if marker == markerSOS {
			return nil, ErrNoOrientation
		}
		segLen := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(b) {
			return nil, ErrNoOrientation
		}
		payload := b[i+4 : i+2+segLen]
		if marker == markerAPP1 && len(payload) >= 6 &&
			string(payload[:6]) == "Exif\x00\x00" {
			return payload[6:], nil
		}
		i += 2 + segLen
	}
	return nil, ErrNoOrientation
}

// parseOrientation walks the TIFF header + IFD0 of an EXIF payload to find the
// Orientation tag. It checks every offset to stay panic-free on hostile input.
func parseOrientation(tiff []byte) (int, error) {
	if len(tiff) < 8 {
		return 0, ErrNoOrientation
	}
	bo, err := byteOrder(tiff)
	if err != nil {
		return 0, err
	}
	ifdOff := int(bo.Uint32(tiff[4:8]))
	if ifdOff < 8 || ifdOff+2 > len(tiff) {
		return 0, ErrNoOrientation
	}
	count := int(bo.Uint16(tiff[ifdOff : ifdOff+2]))
	return scanEntries(tiff, bo, ifdOff+2, count)
}

// byteOrder reads the TIFF endianness marker ("II"/"MM").
func byteOrder(tiff []byte) (binary.ByteOrder, error) {
	switch string(tiff[:2]) {
	case "II":
		return binary.LittleEndian, nil
	case "MM":
		return binary.BigEndian, nil
	default:
		return nil, ErrNoOrientation
	}
}

// scanEntries reads up to count 12-byte IFD entries looking for Orientation.
func scanEntries(tiff []byte, bo binary.ByteOrder, start, count int) (int, error) {
	for n := range count {
		off := start + n*12
		if off+12 > len(tiff) {
			break
		}
		if bo.Uint16(tiff[off:off+2]) != tagOrient {
			continue
		}
		val := int(bo.Uint16(tiff[off+8 : off+10]))
		if val >= 1 && val <= 8 {
			return val, nil
		}
		return 0, ErrNoOrientation
	}
	return 0, ErrNoOrientation
}

// Apply bakes EXIF orientation (1-8) into pixels, returning a normalized image.
// Orientation 1 (or anything unrecognized) returns src unchanged.
func Apply(src image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return flipH(src)
	case 3:
		return rotate180(src)
	case 4:
		return flipV(src)
	case 5:
		return flipH(rotate90(src))
	case 6:
		return rotate90(src)
	case 7:
		return flipH(rotate270(src))
	case 8:
		return rotate270(src)
	default:
		return src
	}
}

// transform applies fn(dx,dy)->(sx,sy) over the destination bounds.
func transform(src image.Image, w, h int, fn func(x, y int) (int, int)) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	rgba := imageToRGBA(src)
	for y := range h {
		for x := range w {
			sx, sy := fn(x, y)
			dst.Set(x, y, rgba.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

func imageToRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	r := image.NewRGBA(b)
	draw.Draw(r, b, src, b.Min, draw.Src)
	return r
}

func flipH(src image.Image) image.Image {
	w, h := dims(src)
	return transform(src, w, h, func(x, y int) (int, int) { return w - 1 - x, y })
}

func flipV(src image.Image) image.Image {
	w, h := dims(src)
	return transform(src, w, h, func(x, y int) (int, int) { return x, h - 1 - y })
}

func rotate180(src image.Image) image.Image {
	w, h := dims(src)
	return transform(src, w, h, func(x, y int) (int, int) { return w - 1 - x, h - 1 - y })
}

func rotate90(src image.Image) image.Image {
	w, h := dims(src)
	return transform(src, h, w, func(x, y int) (int, int) { return y, h - 1 - x })
}

func rotate270(src image.Image) image.Image {
	w, h := dims(src)
	return transform(src, h, w, func(x, y int) (int, int) { return w - 1 - y, x })
}

func dims(src image.Image) (int, int) {
	b := src.Bounds()
	return b.Dx(), b.Dy()
}
