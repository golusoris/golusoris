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
//	c := ollama.New(ollama.Config{
//	    BaseURL: "http://localhost:11434",
//	    Model:   "llama3.3",
//	})
//	resp, _ := c.Chat(ctx, []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
package ollama

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
	// HTTPClient is optional; when set, replaces the default client.
	HTTPClient *http.Client
}

// Client implements [llm.Client] against Ollama's native API.
type Client struct {
	cfg  Config
	base string
	hc   *http.Client
}

// New returns an Ollama client.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{cfg: cfg, base: strings.TrimRight(cfg.BaseURL, "/"), hc: hc}
}

// Chat implements [llm.Client].
func (c *Client) Chat(ctx context.Context, messages []llm.Message, opts ...llm.Option) (llm.Response, error) {
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return llm.Response{}, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, raw)
	}
	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
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
	ch := make(chan llm.Chunk, 32)
	go func() {
		defer close(ch)
		s := c.resolve(opts)
		body, err := json.Marshal(c.buildChatRequest(s, messages, true))
		if err != nil {
			ch <- llm.Chunk{Err: fmt.Errorf("ollama: marshal: %w", err)}
			return
		}
		resp, err := c.post(ctx, "/api/chat", body)
		if err != nil {
			ch <- llm.Chunk{Err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
			ch <- llm.Chunk{Err: fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, raw)}
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			var ev chatResponse
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Message.Content != "" {
				ch <- llm.Chunk{Content: ev.Message.Content}
			}
			if ev.Done {
				return
			}
		}
	}()
	return ch
}

// Embed implements [llm.Client].
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("ollama: embed HTTP %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
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
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}
