// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package cv defines a backend-neutral computer-vision contract.
//
// The repository does not ship an OpenCV runtime backend. Applications provide
// an [Analyzer] implementation. [NewAnalyzer] fails explicitly so an incomplete
// prototype cannot be mistaken for a working optional build.
package cv

import (
	"context"
	"errors"
	"image"
)

// ErrCGORequired is returned because no runtime CV backend is bundled.
// The name is retained for API compatibility.
var ErrCGORequired = errors.New("cv: no runtime backend is bundled; inject an Analyzer")

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

// Options is retained for the compatibility [NewAnalyzer] constructor. It has
// no effect while the repository ships no runtime backend.
type Options struct {
	// FaceModelPath is reserved for application backends.
	FaceModelPath string
	// ObjectModelConfig and ObjectModelWeights are reserved for application
	// backends.
	ObjectModelConfig  string
	ObjectModelWeights string
	// ConfidenceThreshold is reserved for application backends.
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

// NewAnalyzer returns [ErrCGORequired]. Applications must provide an [Analyzer].
func NewAnalyzer(_ Options) (Analyzer, error) { return stub{}, ErrCGORequired }
