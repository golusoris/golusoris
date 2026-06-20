# Agent guide — media/cv/

Computer-vision helpers (face detection, object detection, video thumbnailing)
backed by OpenCV 4 via gocv (CGO). Direct-import constructor — **no fx wiring**;
apps build an `Analyzer` and share it.

## API

```go
a, err := cv.NewAnalyzer(cv.Options{ConfidenceThreshold: 0.5})
defer a.Close()                                  // releases OpenCV resources

faces, err := a.DetectFaces(ctx, src)            // []Face (image.Rectangle bounds)
objs,  err := a.DetectObjects(ctx, src)          // []Detection (label, conf, bounds)
thumb, err := a.Thumbnail(ctx, "clip.mp4", 3.0)  // frame at offsetSec
```

`Options` overrides model paths: `FaceModelPath` (Haar cascade XML),
`ObjectModelConfig` + `ObjectModelWeights` (DNN, e.g. YOLOv4 / SSD MobileNet).

## Why gocv

- The maintained Go binding to OpenCV 4 with DNN-module support; covers classic
  cascades and modern detection nets behind one CGO surface.

## Notes

- **CGO-gated, own go.mod sub-module.** Shipped `NewAnalyzer` is a stub returning
  `ErrCGORequired`; activate per the package doc (drop `//go:build ignore` from
  `impl_gocv.go`, `go get gocv.io/x/gocv`, `go mod tidy`). Requires `libopencv-dev`
  (apt) / `opencv` (brew).
- Object detection needs model config + weights supplied via `Options`; there is
  no bundled network. `DetectObjects` returns nothing useful without them.
- `Close()` once at teardown, not per request.
