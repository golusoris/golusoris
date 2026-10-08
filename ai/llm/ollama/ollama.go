// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ollama implements the [llm.Client] interface against
// Ollama's native API (/api/chat, /api/generate, /api/embeddings).
//
// Ollama also exposes an OpenAI-compatible endpoint — for basic chat,
// the top-level [ai/llm.OpenAIClient] pointed at
// "http://localhost:11434/v1" works fine. This sub-package adds the
// native API so apps can:
//
//   - use Ollama-specific options (num_ctx, num_gpu, keep_alive)
//   - stream via Ollama's NDJSON format (slightly simpler than SSE)
//   - call /api/embeddings (the OpenAI-compat endpoint sometimes lags
//     behind on embedding-model support)
//
// Usage:
//
//	c, err := ollama.New(ollama.Config{
//	    BaseURL: "http://localhost:11434",
//	    Model:   "llama3.3",
//	})
//	if err != nil { /* handle invalid configuration */ }
//	resp, _ := c.Chat(ctx, []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golusoris/golusoris/ai/llm"
	gerr "github.com/golusoris/golusoris/core/errors"
	httpclient "github.com/golusoris/golusoris/httpx/client"
)

// DefaultBaseURL is Ollama's default local endpoint.
const DefaultBaseURL = "http://localhost:11434"

// Config configures the Ollama client.
type Config struct {
	// BaseURL is the Ollama API root. Default [DefaultBaseURL].
	BaseURL string `koanf:"base_url"`
	// Model is the default model name (e.g. "llama3.3", "qwen2.5:14b").
	Model string `koanf:"model"`
	// EmbedModel is used for [Client.Embed]. Falls back to Model when empty.
	EmbedModel string `koanf:"embed_model"`
	// KeepAlive controls how long Ollama keeps the model loaded after a
	// request (e.g. "5m", "-1" for forever, "0" to unload immediately).
	// Empty uses Ollama's server default.
	KeepAlive string `koanf:"keep_alive"`
	// Timeout is the HTTP client timeout. Default 120s.
	Timeout time.Duration `koanf:"timeout"`
	// HTTPClient supplies transport, redirect, and cookie policy. New clones it
	// and applies Timeout when source timeout is non-positive.
	HTTPClient *http.Client
	// MaxResponseBytes caps each successful non-streaming response. Zero uses
	// [llm.DefaultMaxResponseBytes].
	MaxResponseBytes int64 `koanf:"max_response_bytes"`
	// MaxErrorBytes caps each non-success response. Zero uses
	// [llm.DefaultMaxErrorBytes].
	MaxErrorBytes int64 `koanf:"max_error_bytes"`
	// MaxStreamFrameBytes caps one NDJSON frame. Zero uses
	// [llm.DefaultMaxStreamFrameBytes].
	MaxStreamFrameBytes int `koanf:"max_stream_frame_bytes"`
}

// Client implements [llm.Client] against Ollama's native API.
type Client struct {
	cfg  Config
	base string
	hc   *http.Client
}

// New returns an Ollama client.
func New(cfg Config) (*Client, error) {
	bounds, err := llm.NormalizeHTTPBounds(llm.HTTPBounds{
		Timeout: cfg.Timeout, MaxResponseBytes: cfg.MaxResponseBytes,
		MaxErrorBytes: cfg.MaxErrorBytes, MaxStreamFrameBytes: cfg.MaxStreamFrameBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: validate HTTP bounds: %w", err)
	}
	cfg.Timeout = bounds.Timeout
	cfg.MaxResponseBytes = bounds.MaxResponseBytes
	cfg.MaxErrorBytes = bounds.MaxErrorBytes
	cfg.MaxStreamFrameBytes = bounds.MaxStreamFrameBytes
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	hc := httpclient.CloneBounded(cfg.HTTPClient, cfg.Timeout)
	return &Client{cfg: cfg, base: strings.TrimRight(cfg.BaseURL, "/"), hc: hc}, nil
}

// Chat implements [llm.Client].
func (c *Client) Chat(ctx context.Context, messages []llm.Message, opts ...llm.Option) (_ llm.Response, err error) {
	s := c.resolve(opts)
	req := c.buildChatRequest(s, messages, false)
	body, err := json.Marshal(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: marshal: %w", err)
	}
	resp, err := c.post(ctx, "/api/chat", body)
	if err != nil {
		return llm.Response{}, err
	}
	defer func() { gerr.CloseInto(resp.Body, &err, "ollama: close response body") }()
	if resp.StatusCode != http.StatusOK {
		raw, readErr := httpclient.ReadAllBounded(resp.Body, c.cfg.MaxErrorBytes)
		if readErr != nil {
			return llm.Response{}, fmt.Errorf("ollama: HTTP %d error body: %w", resp.StatusCode, readErr)
		}
		return llm.Response{}, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, raw)
	}
	var out chatResponse
	raw, err := httpclient.ReadAllBounded(resp.Body, c.cfg.MaxResponseBytes)
	if err != nil {
		return llm.Response{}, fmt.Errorf("ollama: read response: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return llm.Response{}, fmt.Errorf("ollama: decode: %w", err)
	}
	return llm.Response{
		Content:      out.Message.Content,
		Model:        out.Model,
		InputTokens:  out.PromptEvalCount,
		OutputTokens: out.EvalCount,
	}, nil
}

// Stream implements [llm.Client]. Ollama emits NDJSON — one JSON object
// per line, terminated by {"done":true}.
func (c *Client) Stream(ctx context.Context, messages []llm.Message, opts ...llm.Option) <-chan llm.Chunk {
	return llm.RunStreamContext(ctx, func(ch chan<- llm.Chunk) error {
		return c.stream(ctx, messages, opts, ch)
	})
}

// stream performs one NDJSON request and forwards content onto ch.
func (c *Client) stream(ctx context.Context, messages []llm.Message, opts []llm.Option, ch chan<- llm.Chunk) (err error) {
	s := c.resolve(opts)
	body, err := json.Marshal(c.buildChatRequest(s, messages, true))
	if err != nil {
		return fmt.Errorf("ollama: marshal: %w", err)
	}
	resp, err := c.post(ctx, "/api/chat", body)
	if err != nil {
		return err
	}
	defer func() { gerr.CloseInto(resp.Body, &err, "ollama: close stream body") }()
	if resp.StatusCode != http.StatusOK {
		raw, readErr := httpclient.ReadAllBounded(resp.Body, c.cfg.MaxErrorBytes)
		if readErr != nil {
			return fmt.Errorf("ollama: HTTP %d error body: %w", resp.StatusCode, readErr)
		}
		return fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, raw)
	}
	scanErr := llm.ScanStreamLines(resp.Body, c.cfg.MaxStreamFrameBytes, func(line []byte) (bool, error) {
		return handleStreamLine(ctx, ch, line)
	})
	if scanErr != nil {
		return fmt.Errorf("ollama: stream: %w", scanErr)
	}
	return nil
}

func handleStreamLine(ctx context.Context, ch chan<- llm.Chunk, line []byte) (bool, error) {
	event, ok, err := parseStreamLine(line)
	if err != nil || !ok {
		return false, err
	}
	if event.Error != "" {
		return false, fmt.Errorf("ollama stream provider error: %s", event.Error)
	}
	if event.Message.Content != "" {
		if err = llm.SendChunk(ctx, ch, llm.Chunk{Content: event.Message.Content}); err != nil {
			return false, fmt.Errorf("ollama: send delta: %w", err)
		}
	}
	return event.Done, nil
}

func parseStreamLine(line []byte) (chatResponse, bool, error) {
	if len(strings.TrimSpace(string(line))) == 0 {
		return chatResponse{}, false, nil
	}
	var event chatResponse
	if err := json.Unmarshal(line, &event); err != nil {
		return chatResponse{}, false, fmt.Errorf("invalid Ollama stream event: %w", err)
	}
	return event, true, nil
}

// Embed implements [llm.Client].
func (c *Client) Embed(ctx context.Context, text string) (_ []float32, err error) {
	model := c.cfg.EmbedModel
	if model == "" {
		model = c.cfg.Model
	}
	body, err := json.Marshal(map[string]any{
		"model":  model,
		"prompt": text,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama: embed marshal: %w", err)
	}
	resp, err := c.post(ctx, "/api/embeddings", body)
	if err != nil {
		return nil, err
	}
	defer func() { gerr.CloseInto(resp.Body, &err, "ollama: close embed body") }()
	if resp.StatusCode != http.StatusOK {
		raw, readErr := httpclient.ReadAllBounded(resp.Body, c.cfg.MaxErrorBytes)
		if readErr != nil {
			return nil, fmt.Errorf("ollama: embed HTTP %d error body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("ollama: embed HTTP %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Embedding []float32 `json:"embedding"`
	}
	raw, err := httpclient.ReadAllBounded(resp.Body, c.cfg.MaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("ollama: embed read response: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ollama: embed decode: %w", err)
	}
	if len(out.Embedding) == 0 {
		return nil, errors.New("ollama: embed: empty vector")
	}
	return out.Embedding, nil
}

func (c *Client) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: request: %w", err)
	}
	return resp, nil
}

func (c *Client) resolve(opts []llm.Option) llm.Settings {
	return llm.Resolve(llm.Settings{Model: c.cfg.Model}, opts)
}

func (c *Client) buildChatRequest(s llm.Settings, messages []llm.Message, stream bool) map[string]any {
	msgs := make([]map[string]string, 0, len(messages)+1)
	if s.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": s.System})
	}
	for _, m := range messages {
		msgs = append(msgs, map[string]string{"role": string(m.Role), "content": m.Content})
	}
	options := map[string]any{
		"temperature": s.Temperature,
	}
	if s.MaxTokens > 0 {
		options["num_predict"] = s.MaxTokens
	}
	req := map[string]any{
		"model":    s.Model,
		"messages": msgs,
		"stream":   stream,
		"options":  options,
	}
	if c.cfg.KeepAlive != "" {
		req["keep_alive"] = c.cfg.KeepAlive
	}
	return req
}

type chatResponse struct {
	Model   string `json:"model"`
	Done    bool   `json:"done"`
	Error   string `json:"error"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}
