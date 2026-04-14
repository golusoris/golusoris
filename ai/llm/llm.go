// Package llm provides a provider-agnostic LLM client interface and an
// HTTP backend compatible with OpenAI-format APIs (OpenAI, Azure OpenAI,
// Ollama, LM Studio, Groq, Mistral, …).
//
// For provider-specific features (Anthropic thinking, tool use, vision),
// see sub-packages: ai/llm/anthropic, ai/llm/openai.
//
// Usage:
//
//	client := llm.NewOpenAIClient(llm.Config{
//	    BaseURL: "https://api.openai.com/v1",
//	    APIKey:  os.Getenv("OPENAI_API_KEY"),
//	    Model:   "gpt-4o-mini",
//	})
//
//	resp, err := client.Chat(ctx, []llm.Message{
//	    {Role: llm.RoleUser, Content: "Summarise this article: " + text},
//	})
//
//	for chunk := range client.Stream(ctx, messages) {
//	    fmt.Print(chunk.Content)
//	}
package llm

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
)

// Role is the speaker role in a conversation.
type Role string

// Conversation role constants.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in a conversation.
type Message struct {
	Role    Role
	Content string
}

// Response is a completed generation.
type Response struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
}

// Chunk is a streaming delta.
type Chunk struct {
	Content string
	Err     error // non-nil signals stream error; final chunk has empty Content + nil Err
}

// Option tweaks a single generation.
type Option func(*genOptions)

type genOptions struct {
	model       string
	maxTokens   int
	temperature float64
	system      string
}

// WithModel overrides the default model for this request.
func WithModel(model string) Option { return func(o *genOptions) { o.model = model } }

// WithMaxTokens sets the maximum output token budget.
func WithMaxTokens(n int) Option { return func(o *genOptions) { o.maxTokens = n } }

// WithTemperature sets sampling temperature (0 = deterministic, 1 = creative).
func WithTemperature(t float64) Option { return func(o *genOptions) { o.temperature = t } }

// WithSystem sets a system prompt that is prepended to the message list.
func WithSystem(prompt string) Option { return func(o *genOptions) { o.system = prompt } }

// Client is the LLM capability interface.
type Client interface {
	// Chat returns a complete response for the message thread.
	Chat(ctx context.Context, messages []Message, opts ...Option) (Response, error)
	// Stream returns a channel that emits content deltas. The channel is
	// closed when the generation completes or an error occurs.
	Stream(ctx context.Context, messages []Message, opts ...Option) <-chan Chunk
	// Embed returns a vector embedding for text.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Config configures an OpenAI-compatible HTTP client.
type Config struct {
	// BaseURL is the API root (e.g. "https://api.openai.com/v1").
	BaseURL string
	// APIKey is sent as Bearer token.
	APIKey string
	// Model is the default model name.
	Model string
	// EmbedModel is used for Embed calls. Falls back to Model when empty.
	EmbedModel string
	// Timeout is the HTTP client timeout. Default: 120s.
	Timeout time.Duration
}

// OpenAIClient is an HTTP Client implementing the OpenAI chat completions API.
type OpenAIClient struct {
	cfg  Config
	http *http.Client
}

// NewOpenAIClient returns a Client targeting any OpenAI-compatible endpoint.
func NewOpenAIClient(cfg Config) *OpenAIClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second
	}
	return &OpenAIClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

// Chat implements [Client].
func (c *OpenAIClient) Chat(ctx context.Context, messages []Message, opts ...Option) (Response, error) {
	o := c.applyOpts(opts)
	body, err := json.Marshal(chatRequest(o.model, messages, o, false))
	if err != nil {
		return Response{}, fmt.Errorf("llm: marshal: %w", err)
	}

	req, err := c.newRequest(ctx, "/chat/completions", body)
	if err != nil {
		return Response{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("llm: request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Response{}, fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, raw)
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Response{}, fmt.Errorf("llm: decode: %w", err)
	}
	content := ""
	if len(out.Choices) > 0 {
		content = out.Choices[0].Message.Content
	}
	return Response{
		Content:      content,
		Model:        out.Model,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}, nil
}

// Stream implements [Client]. The channel is closed when streaming ends.
func (c *OpenAIClient) Stream(ctx context.Context, messages []Message, opts ...Option) <-chan Chunk {
	ch := make(chan Chunk, 32)
	o := c.applyOpts(opts)
	go func() {
		defer close(ch)
		body, err := json.Marshal(chatRequest(o.model, messages, o, true))
		if err != nil {
			ch <- Chunk{Err: fmt.Errorf("llm: marshal: %w", err)}
			return
		}
		req, err := c.newRequest(ctx, "/chat/completions", body)
		if err != nil {
			ch <- Chunk{Err: err}
			return
		}
		resp, err := c.http.Do(req)
		if err != nil {
			ch <- Chunk{Err: fmt.Errorf("llm: request: %w", err)}
			return
		}
		defer resp.Body.Close() //nolint:errcheck

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			ch <- Chunk{Err: fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, raw)}
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return
			}
			var ev struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if len(ev.Choices) > 0 && ev.Choices[0].Delta.Content != "" {
				ch <- Chunk{Content: ev.Choices[0].Delta.Content}
			}
		}
	}()
	return ch
}

// Embed implements [Client]. Returns a 1536-dim vector for text-embedding-3-small
// or whatever the configured EmbedModel supports.
func (c *OpenAIClient) Embed(ctx context.Context, text string) ([]float32, error) {
	model := c.cfg.EmbedModel
	if model == "" {
		model = c.cfg.Model
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"input": text,
	})
	if err != nil {
		return nil, fmt.Errorf("llm: embed marshal: %w", err)
	}
	req, err := c.newRequest(ctx, "/embeddings", body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: embed request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm: embed HTTP %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("llm: embed decode: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, errors.New("llm: embed: empty response")
	}
	return out.Data[0].Embedding, nil
}

func (c *OpenAIClient) newRequest(ctx context.Context, path string, body []byte) (*http.Request, error) {
	url := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	return req, nil
}

func (c *OpenAIClient) applyOpts(opts []Option) *genOptions {
	o := &genOptions{
		model:       c.cfg.Model,
		temperature: 1.0,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func chatRequest(model string, messages []Message, o *genOptions, stream bool) map[string]any {
	msgs := make([]map[string]string, 0, len(messages)+1)
	if o.system != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": o.system})
	}
	for _, m := range messages {
		msgs = append(msgs, map[string]string{"role": string(m.Role), "content": m.Content})
	}
	req := map[string]any{
		"model":       model,
		"messages":    msgs,
		"temperature": o.temperature,
		"stream":      stream,
	}
	if o.maxTokens > 0 {
		req["max_tokens"] = o.maxTokens
	}
	return req
}
