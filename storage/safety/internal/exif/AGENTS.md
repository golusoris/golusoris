# Agent guide — storage/safety/internal/exif/

**Internal** package (not for direct app import). Reads *only* the JPEG EXIF
Orientation tag and bakes the corresponding rotation/flip into pixels.
Deliberately minimal — a full EXIF parser is out of scope, because the parent
`storage/safety` strips all metadata by re-encoding; this exists so a phone photo
shot rotated doesn't display sideways after that strip.

## API

```go
o, err := exif.Orientation(jpegBytes) // 1..8 (1 = none); err on malformed
img = exif.Apply(srcImage, o)         // returns a pixel-rotated copy
```

## Notes

- Pure Go, panic-safe, both byte orders (big/little-endian TIFF). Covers
  orientations 1–8; out-of-range / no-tag → orientation 1 (no-op).
- Security-critical context (untrusted upload bytes); kept tiny + fuzz-adjacent.
- See ADR-0008 (upload safety) for why orientation is read but the rest of EXIF
  is stripped, not parsed.
