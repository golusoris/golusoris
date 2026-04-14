//go:build ignore
// +build ignore

// Activate: remove the go:build ignore line above, then:
//   go get gocv.io/x/gocv
//   go mod tidy

package cv

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"

	"gocv.io/x/gocv"
)

func wrapf(format string, a ...any) error { return fmt.Errorf("cv: "+format, a...) }

type gocvAnalyzer struct {
	opts       Options
	faceModel  gocv.CascadeClassifier
}

// NewAnalyzer returns an OpenCV-backed Analyzer.
func NewAnalyzer(opts Options) (Analyzer, error) {
	faceModelPath := opts.FaceModelPath
	if faceModelPath == "" {
		// OpenCV ships this via gocv.
		faceModelPath = "haarcascade_frontalface_default.xml"
	}
	cc := gocv.NewCascadeClassifier()
	if !cc.Load(faceModelPath) {
		return nil, fmt.Errorf("cv: load face cascade %s", faceModelPath)
	}
	return &gocvAnalyzer{opts: opts, faceModel: cc}, nil
}

func (a *gocvAnalyzer) DetectFaces(_ context.Context, src []byte) ([]Face, error) {
	mat, err := gocv.IMDecode(src, gocv.IMReadColor)
	if err != nil {
		return nil, wrapf("decode: %w", err)
	}
	defer mat.Close()

	rects := a.faceModel.DetectMultiScale(mat)
	faces := make([]Face, len(rects))
	for i, r := range rects {
		faces[i] = Face{Bounds: image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)}
	}
	return faces, nil
}

func (a *gocvAnalyzer) DetectObjects(_ context.Context, _ []byte) ([]Detection, error) {
	if a.opts.ObjectModelConfig == "" || a.opts.ObjectModelWeights == "" {
		return nil, fmt.Errorf("cv: ObjectModelConfig and ObjectModelWeights required for object detection")
	}
	// DNN-based detection requires additional setup (class labels, NMS, etc.).
	// Implement per your model; this stub shows the gocv entry points.
	return nil, fmt.Errorf("cv: object detection not yet implemented; set ObjectModelConfig/Weights")
}

func (a *gocvAnalyzer) Thumbnail(_ context.Context, videoPath string, offsetSec float64) ([]byte, error) {
	vc, err := gocv.VideoCaptureFile(videoPath)
	if err != nil {
		return nil, wrapf("open video: %w", err)
	}
	defer vc.Close()

	fps := vc.Get(gocv.VideoCaptureFPS)
	if fps <= 0 {
		fps = 25
	}
	targetFrame := int(offsetSec * fps)
	vc.Set(gocv.VideoCapturePosFrames, float64(targetFrame))

	frame := gocv.NewMat()
	defer frame.Close()
	if ok := vc.Read(&frame); !ok || frame.Empty() {
		return nil, fmt.Errorf("cv: could not read frame at offset %.1fs", offsetSec)
	}

	img, err := frame.ToImage()
	if err != nil {
		return nil, wrapf("to image: %w", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, wrapf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

func (a *gocvAnalyzer) Close() { a.faceModel.Close() }
