// Package tflite implements a [tiny.Predictor] that serves LiteRT
// (`.tflite`) classifiers produced by [ai/tiny/litert] via a Python
// inference sidecar over HTTP.
//
// Why a sidecar, not in-process: LiteRT inference in Go requires CGo
// bindings to libtensorflowlite, which drags a C toolchain + platform
// build matrix into every consumer. Mirroring the ollama adapter, this
// package is a thin HTTP client to a long-running sidecar
// (`ghcr.io/golusoris/tiny-litert-server`) that holds the interpreter.
// The sidecar loads the `.tflite` artifact referenced by [tiny.Model.URI]
// and exposes:
//
//   - POST /load     {"uri": "...", "labels": [...]}  → 200 on success
//   - POST /classify {"input": <any>}                  → {"scores": {label: prob}}
//   - GET  /healthz                                    → 200 when ready
//
// The Go predictor validates the model is a classifier, registers it
// with the sidecar on Load, and maps the sidecar's score map onto a
// sorted [tiny.Prediction.Labels] slice.
//
// Native runtime status: a pure-Go in-process backend is deferred — the
// sidecar contract is the supported path. The interface below is stable;
// a future in-process [Predictor] can satisfy it without a consumer
// change.
package tflite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golusoris/golusoris/ai/tiny"
)

// DefaultEndpoint is the LiteRT sidecar HTTP root.
const DefaultEndpoint = "http://127.0.0.1:8501"

// DefaultMaxResponseBytes caps a /classify response body. Classifier
// outputs are a small label→score map; 256 KiB is generous.
const DefaultMaxResponseBytes int64 = 256 << 10

// Options configures a [Predictor].
type Options struct {
	// Endpoint is the LiteRT sidecar HTTP root (default [DefaultEndpoint]).
	Endpoint string
	// HTTPClient defaults to a client with a 30s timeout.
	HTTPClient *http.Client
	// MaxResponseBytes caps the response body read size
	// (default [DefaultMaxResponseBytes]).
	MaxResponseBytes int64
	// TopK, when > 0, truncates the returned label set to the K
	// highest-scoring labels after sorting. 0 = return all.
	TopK int
}

// Predictor routes Predict calls to a LiteRT sidecar's /classify
// endpoint. Safe for concurrent Predict calls after Load returns.
type Predictor struct {
	opts Options

	mu     sync.RWMutex
	loaded bool
	labels []string // ordered label set from the loaded model
	name   string   // tiny.Model.Name (introspection)
}

// NewPredictor returns a Predictor with the given options.
func NewPredictor(opts Options) *Predictor {
	if opts.Endpoint == "" {
		opts.Endpoint = DefaultEndpoint
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.MaxResponseBytes <= 0 {
		opts.MaxResponseBytes = DefaultMaxResponseBytes
	}
	return &Predictor{opts: opts}
}

// loadRequest is the body posted to the sidecar's /load endpoint.
type loadRequest struct {
	URI    string   `json:"uri"`
	Labels []string `json:"labels,omitempty"`
}

// Load validates m is a LiteRT classifier and registers its artifact
// with the sidecar via POST /load. It records the label set for mapping
// scores at Predict time.
func (p *Predictor) Load(ctx context.Context, m tiny.Model) error {
	if m.TaskKind != tiny.TaskClassify {
		return fmt.Errorf("ai/tiny/serve/tflite: TaskKind=%s not supported (want classify)", m.TaskKind)
	}
	if m.Format != tiny.FormatTFLite {
		return fmt.Errorf("ai/tiny/serve/tflite: Format=%s not supported (want tflite)", m.Format)
	}
	if m.URI == "" {
		return errors.New("ai/tiny/serve/tflite: model.URI empty")
	}
	body, mErr := json.Marshal(loadRequest{URI: m.URI, Labels: m.Labels})
	if mErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: marshal load: %w", mErr)
	}
	if err := p.post(ctx, "/load", body, nil); err != nil {
		return err
	}
	p.mu.Lock()
	p.loaded = true
	p.labels = append([]string(nil), m.Labels...)
	p.name = m.Name
	p.mu.Unlock()
	return nil
}

// Predict sends input to the sidecar and maps the returned score map
// onto a sorted [tiny.Prediction.Labels] slice (desc by score). Input is
// passed through verbatim as JSON — the sidecar interprets it per the
// model's modality (text string, image bytes/path, audio samples).
func (p *Predictor) Predict(ctx context.Context, input any) (tiny.Prediction, error) {
	p.mu.RLock()
	loaded := p.loaded
	p.mu.RUnlock()
	if !loaded {
		return tiny.Prediction{}, errors.New("ai/tiny/serve/tflite: Load not called")
	}
	reqBody, mErr := json.Marshal(map[string]any{"input": input})
	if mErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/tflite: marshal request: %w", mErr)
	}
	var out struct {
		Scores map[string]float32 `json:"scores"`
	}
	if err := p.post(ctx, "/classify", reqBody, &out); err != nil {
		return tiny.Prediction{}, err
	}
	if out.Scores == nil {
		return tiny.Prediction{}, errors.New("ai/tiny/serve/tflite: sidecar returned no scores")
	}
	return tiny.Prediction{Labels: p.sortScores(out.Scores)}, nil
}

// sortScores converts the sidecar score map into a slice sorted desc by
// score (ties broken by label for determinism), truncated to TopK.
func (p *Predictor) sortScores(scores map[string]float32) []tiny.LabelScore {
	labels := make([]tiny.LabelScore, 0, len(scores))
	for label, score := range scores {
		labels = append(labels, tiny.LabelScore{Label: label, Score: score})
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].Score != labels[j].Score {
			return labels[i].Score > labels[j].Score
		}
		return labels[i].Label < labels[j].Label
	})
	if p.opts.TopK > 0 && len(labels) > p.opts.TopK {
		labels = labels[:p.opts.TopK]
	}
	return labels
}

// Close is a no-op — the sidecar holds the interpreter; this client has
// no per-session state. The sidecar drops the loaded model on its own
// lifecycle.
func (*Predictor) Close() error { return nil }

// post issues a JSON POST to path and, when out is non-nil, decodes the
// response body into it.
func (p *Predictor) post(ctx context.Context, path string, body []byte, out any) error {
	req, rErr := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.opts.Endpoint, "/")+path, bytes.NewReader(body))
	if rErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: build %s request: %w", path, rErr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, dErr := p.opts.HTTPClient.Do(req)
	if dErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: %s request: %w", path, dErr)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, p.opts.MaxResponseBytes))
	if readErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: read %s body: %w", path, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ai/tiny/serve/tflite: %s status %d: %s", path, resp.StatusCode, truncate(string(respBody), 200))
	}
	if out == nil {
		return nil
	}
	if jErr := json.Unmarshal(respBody, out); jErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: decode %s response: %w", path, jErr)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// compile-time assertion: Predictor satisfies tiny.Predictor.
var _ tiny.Predictor = (*Predictor)(nil)
