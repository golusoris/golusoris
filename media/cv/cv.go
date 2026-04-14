// Package cv provides computer vision helpers backed by OpenCV via gocv (CGO).
// OpenCV 4 must be installed before this package can be used.
//
// Install system deps (Debian/Ubuntu):
//
//	apt-get install libopencv-dev
//
// Install system deps (macOS):
//
//	brew install opencv
//
// Activate implementation:
//
//	1. Remove //go:build ignore from media/cv/impl_gocv.go
//	2. Add the dep: go get gocv.io/x/gocv
//	3. go mod tidy
//
// Usage:
//
//	a, err := cv.NewAnalyzer(cv.Options{})
//	defer a.Close()
//	faces, err := a.DetectFaces(ctx, imageBytes)
//	objs,  err := a.DetectObjects(ctx, imageBytes, cv.COCOModel)
package cv

import (
	"context"
	"errors"
	"image"
)

// ErrCGORequired is returned when the gocv implementation is not activated.
var ErrCGORequired = errors.New("cv: CGO implementation not activated; see package doc")

// Detection is a bounding box + confidence from an object-detection model.
type Detection struct {
	Label      string
	Confidence float32
	Bounds     image.Rectangle
}

// Face is a detected face bounding box.
type Face struct {
	Bounds image.Rectangle
}

// Options configures the CV analyzer.
type Options struct {
	// FaceModelPath overrides the default Haar cascade XML path.
	FaceModelPath string
	// ObjectModelConfig / ObjectModelWeights are paths to the network config +
	// weights for object detection (e.g. YOLOv4, SSD MobileNet).
	ObjectModelConfig  string
	ObjectModelWeights string
	// ConfidenceThreshold filters detections below this level (default 0.5).
	ConfidenceThreshold float32
}

// Analyzer runs CV tasks on image data.
type Analyzer interface {
	// DetectFaces returns bounding boxes for all detected faces.
	DetectFaces(ctx context.Context, src []byte) ([]Face, error)
	// DetectObjects runs an object-detection DNN and returns labelled boxes.
	DetectObjects(ctx context.Context, src []byte) ([]Detection, error)
	// Thumbnail extracts a representative frame from a video file at offset.
	Thumbnail(ctx context.Context, videoPath string, offsetSec float64) ([]byte, error)
	// Close releases OpenCV resources.
	Close()
}

type stub struct{}

func (stub) DetectFaces(_ context.Context, _ []byte) ([]Face, error) {
	return nil, ErrCGORequired
}
func (stub) DetectObjects(_ context.Context, _ []byte) ([]Detection, error) {
	return nil, ErrCGORequired
}
func (stub) Thumbnail(_ context.Context, _ string, _ float64) ([]byte, error) {
	return nil, ErrCGORequired
}
func (stub) Close() {}

// NewAnalyzer returns an [Analyzer] backed by OpenCV.
// When the CGO implementation is not activated it returns an error.
func NewAnalyzer(_ Options) (Analyzer, error) { return stub{}, ErrCGORequired }
