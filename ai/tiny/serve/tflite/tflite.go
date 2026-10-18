// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tflite implements a [tiny.Predictor] that serves LiteRT
// (`.tflite`) image classifiers produced by [ai/tiny/litert] via a Python
// inference sidecar over HTTP.
//
// Why a sidecar, not in-process: LiteRT inference in Go requires CGo
// bindings to libtensorflowlite, which drags a C toolchain + platform
// build matrix into every consumer. Mirroring the ollama adapter, this
// package is a thin HTTP client to a sidecar runtime that holds the
// interpreter. The sidecar implementation lives under
// ai/tiny/serve/tflite/sidecar. The sidecar loads the `.tflite` artifact
// referenced by [tiny.Model.URI] and exposes:
//
//   - POST /load     {"uri": "...", "sha256": "...", "labels": [...]} → 200 on success
//   - POST /classify {"image": "<base64>"}                            → {"scores": {label: prob}}
//   - GET  /healthz                                                   → 200 when ready
//
// The Go predictor validates the model is an image classifier, registers it
// with the sidecar on Load, and maps the sidecar's score map onto a
// sorted [tiny.Prediction.Labels] slice.
package tflite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/internal/httpoptions"
	gerr "github.com/golusoris/golusoris/core/errors"
	httpclient "github.com/golusoris/golusoris/httpx/client"
)

// DefaultEndpoint is the LiteRT sidecar HTTP root.
const DefaultEndpoint = "http://127.0.0.1:8501"

// DefaultMaxResponseBytes caps a /classify response body. Classifier
// outputs are a small label→score map; 256 KiB is generous.
const DefaultMaxResponseBytes int64 = 256 << 10

const defaultRequestTimeout = 30 * time.Second

// Options configures a [Predictor].
type Options struct {
	// Endpoint is the LiteRT sidecar HTTP root (default [DefaultEndpoint]).
	Endpoint string
	// HTTPClient is cloned; a non-positive timeout becomes 30s.
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
	labels map[string]struct{} // immutable loaded classifier label set
	name   string              // tiny.Model.Name (introspection)
}

// NewPredictor returns a Predictor with the given options.
func NewPredictor(opts Options) *Predictor {
	opts.Endpoint, opts.HTTPClient, opts.MaxResponseBytes = httpoptions.Normalize(
		opts.Endpoint, opts.HTTPClient, opts.MaxResponseBytes,
		httpoptions.Defaults{
			Endpoint:         DefaultEndpoint,
			RequestTimeout:   defaultRequestTimeout,
			MaxResponseBytes: DefaultMaxResponseBytes,
		},
	)
	return &Predictor{opts: opts}
}

// loadRequest is the body posted to the sidecar's /load endpoint.
type loadRequest struct {
	URI    string   `json:"uri"`
	SHA256 string   `json:"sha256"`
	Labels []string `json:"labels,omitempty"`
}

// classifyRequest is the typed body posted to the sidecar's /classify endpoint.
type classifyRequest struct {
	Image string `json:"image"`
}

// Load validates m is a LiteRT image classifier and registers its artifact
// with the sidecar via POST /load. It records the label set for mapping
// scores at Predict time.
func (p *Predictor) Load(ctx context.Context, m tiny.Model) error {
	if m.TaskKind != tiny.TaskClassify {
		return fmt.Errorf("ai/tiny/serve/tflite: load: TaskKind=%s not supported (want classify)", m.TaskKind)
	}
	if m.Format != tiny.FormatTFLite {
		return fmt.Errorf("ai/tiny/serve/tflite: load: Format=%s not supported (want tflite)", m.Format)
	}
	if m.Modality != tiny.ModalityImage {
		return fmt.Errorf("ai/tiny/serve/tflite: load: Modality=%s not supported (want image)", m.Modality)
	}
	if err := tiny.ValidateClassifierLabels(m.Labels); err != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: load: validate labels: %w", err)
	}
	if m.URI == "" {
		return errors.New("ai/tiny/serve/tflite: load: model.URI empty")
	}
	sha256 := m.Metadata["sha256"]
	if sha256 == "" {
		return errors.New("ai/tiny/serve/tflite: load: model metadata sha256 empty")
	}
	body, mErr := json.Marshal(loadRequest{
		URI:    m.URI,
		SHA256: sha256,
		Labels: m.Labels,
	})
	if mErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: load: marshal load: %w", mErr)
	}
	if err := p.post(ctx, "/load", body, nil); err != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: load: %w", err)
	}
	labels := make(map[string]struct{}, len(m.Labels))
	for _, label := range m.Labels {
		labels[label] = struct{}{}
	}
	p.mu.Lock()
	p.loaded = true
	p.labels = labels
	p.name = m.Name
	p.mu.Unlock()
	return nil
}

// Predict sends the typed image input to the sidecar, verifies the response
// contains exactly the loaded model's labels with finite probabilities in [0,1],
// and maps it onto a sorted [tiny.Prediction.Labels] slice (desc by score).
// Input must be encoded image bytes ([]byte) or a base64 string.
func (p *Predictor) Predict(ctx context.Context, input any) (tiny.Prediction, error) {
	p.mu.RLock()
	loaded := p.loaded
	labels := p.labels
	p.mu.RUnlock()
	if !loaded {
		return tiny.Prediction{}, errors.New("ai/tiny/serve/tflite: predict: Load not called")
	}
	imageB64, err := extractImageBase64(input)
	if err != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/tflite: predict: %w", err)
	}
	reqBody, mErr := json.Marshal(classifyRequest{Image: imageB64})
	if mErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/tflite: predict: marshal request: %w", mErr)
	}
	var out struct {
		Scores map[string]float32 `json:"scores"`
	}
	if err := p.post(ctx, "/classify", reqBody, &out); err != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/tflite: predict: %w", err)
	}
	if out.Scores == nil {
		return tiny.Prediction{}, errors.New("ai/tiny/serve/tflite: predict: sidecar returned no scores")
	}
	if err := validateScores(out.Scores, labels); err != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/tflite: predict: %w", err)
	}
	return tiny.Prediction{Labels: p.sortScores(out.Scores)}, nil
}

func extractImageBase64(input any) (string, error) {
	switch v := input.(type) {
	case []byte:
		if len(v) == 0 {
			return "", errors.New("empty image bytes")
		}
		return base64.StdEncoding.EncodeToString(v), nil
	case string:
		clean := strings.TrimSpace(v)
		if clean == "" {
			return "", errors.New("empty image string")
		}
		return clean, nil
	default:
		return "", fmt.Errorf("unsupported input type %T (want []byte or base64 string)", input)
	}
}

func validateScores(scores map[string]float32, labels map[string]struct{}) error {
	if len(scores) != len(labels) {
		return fmt.Errorf(
			"sidecar label count %d does not match loaded model %d",
			len(scores), len(labels),
		)
	}
	for label, score := range scores {
		if _, loaded := labels[label]; !loaded {
			return fmt.Errorf("sidecar label %q was not loaded", label)
		}
		if score < 0 || score > 1 || math.IsNaN(float64(score)) || math.IsInf(float64(score), 0) {
			return fmt.Errorf("sidecar probability for %q is outside [0,1]", label)
		}
	}
	return nil
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
func (p *Predictor) post(ctx context.Context, path string, body []byte, out any) (err error) {
	req, rErr := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.opts.Endpoint, "/")+path, bytes.NewReader(body))
	if rErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: post: build %s request: %w", path, rErr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, dErr := p.opts.HTTPClient.Do(req)
	if dErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: post: %s request: %w", path, dErr)
	}
	defer func() { gerr.CloseInto(resp.Body, &err, "ai/tiny/serve/tflite: close "+path+" body") }()
	respBody, readErr := httpclient.ReadAllBounded(resp.Body, p.opts.MaxResponseBytes)
	if readErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: post: read %s body: %w", path, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ai/tiny/serve/tflite: post: %s status %d: %s", path, resp.StatusCode, truncate(string(respBody), 200))
	}
	if out == nil {
		return nil
	}
	if jErr := json.Unmarshal(respBody, out); jErr != nil {
		return fmt.Errorf("ai/tiny/serve/tflite: post: decode %s response: %w", path, jErr)
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
