// Package ollama implements a [tiny.Predictor] that serves Gemma
// (base + LoRA) via an [ollama](https://ollama.com) endpoint.
//
// The LoRA bundle produced by [ai/tiny/gemma] is expected to already
// be registered with the ollama instance (via a Modelfile that
// references the base checkpoint + LoRA adapter). This predictor is a
// thin HTTP client — it does not push weights into ollama.
//
// ModelName resolution: the `[tiny.Model].Name` field is used as the
// ollama model tag when Load is called. Applications that need
// versioning should publish distinct ollama tags per version
// (e.g. `intent-v3`).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golusoris/golusoris/ai/tiny"
)

// DefaultEndpoint is the ollama HTTP API root.
const DefaultEndpoint = "http://127.0.0.1:11434"

// DefaultMaxResponseBytes caps /api/generate response size. ollama
// returns JSON with a `response` field — 256 KiB is generous for
// fine-tuned task-specific models.
const DefaultMaxResponseBytes int64 = 256 << 10

// Options configures a [Predictor].
type Options struct {
	// Endpoint is the ollama HTTP API root (default [DefaultEndpoint]).
	Endpoint string
	// HTTPClient defaults to a client with a 60s timeout.
	HTTPClient *http.Client
	// MaxResponseBytes caps the response body read size
	// (default [DefaultMaxResponseBytes]).
	MaxResponseBytes int64
	// TagOverride, when non-empty, overrides the ollama model tag
	// used at Predict time. Useful when the Registry model name
	// differs from the ollama tag.
	TagOverride string
}

// Predictor routes Predict calls to an ollama /api/generate endpoint.
// Safe for concurrent Predict calls after Load returns.
type Predictor struct {
	opts Options

	mu      sync.RWMutex
	loaded  bool
	tag     string // ollama model tag
	rawName string // tiny.Model.Name (for logging / introspection)
}

// NewPredictor returns a Predictor with the given options.
func NewPredictor(opts Options) *Predictor {
	if opts.Endpoint == "" {
		opts.Endpoint = DefaultEndpoint
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.MaxResponseBytes <= 0 {
		opts.MaxResponseBytes = DefaultMaxResponseBytes
	}
	return &Predictor{opts: opts}
}

// Load validates the model is reachable by calling ollama's /api/show.
// It records the tag to use at Predict time.
func (p *Predictor) Load(ctx context.Context, m tiny.Model) error {
	if m.TaskKind != tiny.TaskGenerate {
		return fmt.Errorf("ai/tiny/serve/ollama: TaskKind=%s not supported (want generate)", m.TaskKind)
	}
	if m.Modality != tiny.ModalityText {
		return fmt.Errorf("ai/tiny/serve/ollama: Modality=%s not supported (want text)", m.Modality)
	}
	tag := p.opts.TagOverride
	if tag == "" {
		tag = m.Name
	}
	if tag == "" {
		return errors.New("ai/tiny/serve/ollama: model name empty and no TagOverride")
	}
	if err := p.showModel(ctx, tag); err != nil {
		return err
	}
	p.mu.Lock()
	p.loaded = true
	p.tag = tag
	p.rawName = m.Name
	p.mu.Unlock()
	return nil
}

// Predict sends the prompt to ollama and returns the generated text.
// Input must be a string (prompt).
func (p *Predictor) Predict(ctx context.Context, input any) (tiny.Prediction, error) {
	p.mu.RLock()
	loaded, tag := p.loaded, p.tag
	p.mu.RUnlock()
	if !loaded {
		return tiny.Prediction{}, errors.New("ai/tiny/serve/ollama: Load not called")
	}
	prompt, ok := input.(string)
	if !ok {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/ollama: input must be string, got %T", input)
	}
	reqBody, mErr := json.Marshal(map[string]any{
		"model":  tag,
		"prompt": prompt,
		"stream": false,
	})
	if mErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/ollama: marshal request: %w", mErr)
	}
	req, rErr := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.opts.Endpoint, "/")+"/api/generate", bytes.NewReader(reqBody))
	if rErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/ollama: build request: %w", rErr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, dErr := p.opts.HTTPClient.Do(req)
	if dErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/ollama: request: %w", dErr)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, p.opts.MaxResponseBytes))
	if readErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/ollama: read body: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/ollama: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var out struct {
		Response string `json:"response"`
	}
	if jerr := json.Unmarshal(body, &out); jerr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/ollama: decode response: %w", jerr)
	}
	return tiny.Prediction{Text: out.Response, Raw: json.RawMessage(body)}, nil
}

// Close is a no-op — the HTTP client has no per-session state.
func (*Predictor) Close() error { return nil }

// showModel verifies an ollama model tag is resolvable via /api/show.
func (p *Predictor) showModel(ctx context.Context, tag string) error {
	reqBody, mErr := json.Marshal(map[string]string{"name": tag})
	if mErr != nil {
		return fmt.Errorf("ai/tiny/serve/ollama: marshal show: %w", mErr)
	}
	req, rErr := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.opts.Endpoint, "/")+"/api/show", bytes.NewReader(reqBody))
	if rErr != nil {
		return fmt.Errorf("ai/tiny/serve/ollama: build show: %w", rErr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, dErr := p.opts.HTTPClient.Do(req)
	if dErr != nil {
		return fmt.Errorf("ai/tiny/serve/ollama: show request: %w", dErr)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("ai/tiny/serve/ollama: model %q not registered in ollama (404)", tag)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, p.opts.MaxResponseBytes))
		return fmt.Errorf("ai/tiny/serve/ollama: show status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
