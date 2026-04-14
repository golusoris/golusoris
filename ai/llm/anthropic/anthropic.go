// Package anthropic implements the [llm.Client] interface against
// Anthropic's Messages API.
//
// Raw HTTP — we don't pull in anthropics/anthropic-sdk-go because the
// wire format we need is small, and the SDK's generated surface would
// outweigh the code we actually call.
//
// Anthropic does not expose an embeddings endpoint at the time of
// writing; [Client.Embed] returns an error. Use OpenAI-compatible
// embeddings via [ai/llm.OpenAIClient] for vectors.
//
// Usage:
//
//	c := anthropic.New(anthropic.Config{
//	    APIKey: os.Getenv("ANTHROPIC_API_KEY"),
//	    Model:  "claude-opus-4-1",
//	})
//	resp, _ := c.Chat(ctx, []llm.Message{
//	    {Role: llm.RoleUser, Content: "Hello"},
//	})
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golusoris/golusoris/ai/llm"
)

// DefaultEndpoint is Anthropic's v1 Messages API.
const DefaultEndpoint = "https://api.anthropic.com/v1/messages"

// DefaultVersion pins the anthropic-version header. Apps can override
// via [Config.Version] to opt into newer API behaviour.
const DefaultVersion = "2023-06-01"

// Config configures the Anthropic client.
type Config struct {
	// APIKey is an Anthropic API key. Required.
	APIKey string `koanf:"api_key"`
	// Model is the default Claude model (e.g. "claude-opus-4-6").
	Model string `koanf:"model"`
	// MaxTokens is the default max output tokens. Anthropic requires a
	// value on every request; default 1024 if unset.
	MaxTokens int `koanf:"max_tokens"`
	// Endpoint overrides the API URL (tests).
	Endpoint string `koanf:"endpoint"`
	// Version overrides the anthropic-version header.
	Version string `koanf:"version"`
	// Timeout is the HTTP client timeout. Default 120s.
	Timeout time.Duration `koanf:"timeout"`
	// HTTPClient is optional; when set, replaces the default client.
	HTTPClient *http.Client
}

// Client implements [llm.Client] against Anthropic's API.
type Client struct {
	cfg      Config
	endpoint string
	version  string
	hc       *http.Client
}

// New returns an Anthropic client.
func New(cfg Config) *Client {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	version := cfg.Version
	if version == "" {
		version = DefaultVersion
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 1024
	}
	return &Client{cfg: cfg, endpoint: endpoint, version: version, hc: hc}
}

// Chat implements [llm.Client].
func (c *Client) Chat(ctx context.Context, messages []llm.Message, opts ...llm.Option) (llm.Response, error) {
	o := c.resolve(opts)
	body, err := json.Marshal(c.buildRequest(o, messages, false))
	if err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: marshal: %w", err)
	}
	req, err := c.newRequest(ctx, body)
	if err != nil {
		return llm.Response{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return llm.Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, raw)
	}
	var out messagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: decode: %w", err)
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return llm.Response{
		Content:      sb.String(),
		Model:        out.Model,
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
	}, nil
}

// Stream implements [llm.Client]. Uses Anthropic's SSE streaming.
func (c *Client) Stream(ctx context.Context, messages []llm.Message, opts ...llm.Option) <-chan llm.Chunk {
	ch := make(chan llm.Chunk, 32)
	go func() {
		defer close(ch)
		o := c.resolve(opts)
		body, err := json.Marshal(c.buildRequest(o, messages, true))
		if err != nil {
			ch <- llm.Chunk{Err: fmt.Errorf("anthropic: marshal: %w", err)}
			return
		}
		req, err := c.newRequest(ctx, body)
		if err != nil {
			ch <- llm.Chunk{Err: err}
			return
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			ch <- llm.Chunk{Err: fmt.Errorf("anthropic: request: %w", err)}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
			ch <- llm.Chunk{Err: fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, raw)}
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" || data == "[DONE]" {
				continue
			}
			var ev streamEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				ch <- llm.Chunk{Content: ev.Delta.Text}
			}
		}
	}()
	return ch
}

// Embed implements [llm.Client]. Anthropic does not expose an
// embeddings endpoint at the time of writing.
func (c *Client) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("anthropic: Embed is not supported; use an OpenAI-compatible embeddings endpoint")
}

func (c *Client) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: new request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.cfg.APIKey)
	req.Header.Set("Anthropic-Version", c.version)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) buildRequest(s llm.Settings, messages []llm.Message, stream bool) map[string]any {
	msgs := make([]map[string]string, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, map[string]string{
			"role":    string(m.Role),
			"content": m.Content,
		})
	}
	req := map[string]any{
		"model":       s.Model,
		"max_tokens":  s.MaxTokens,
		"temperature": s.Temperature,
		"messages":    msgs,
		"stream":      stream,
	}
	if s.System != "" {
		req["system"] = s.System
	}
	return req
}

// resolve applies per-request options on top of the client's defaults
// using [llm.Resolve]. Anthropic requires max_tokens on every request,
// so an unset default falls back to 1024.
func (c *Client) resolve(opts []llm.Option) llm.Settings {
	return llm.Resolve(llm.Settings{
		Model:     c.cfg.Model,
		MaxTokens: c.cfg.MaxTokens,
	}, opts)
}

type messagesResponse struct {
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type streamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}
