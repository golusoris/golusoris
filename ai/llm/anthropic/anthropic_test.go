package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/llm"
	"github.com/golusoris/golusoris/ai/llm/anthropic"
)

func TestChat(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-key", r.Header.Get("X-Api-Key"))
		require.Equal(t, "2023-06-01", r.Header.Get("Anthropic-Version"))

		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "claude-opus-4-6", got["model"])
		require.EqualValues(t, 256, got["max_tokens"])
		require.Equal(t, "you are concise", got["system"])
		msgs, _ := got["messages"].([]any)
		require.Len(t, msgs, 1)

		_, _ = w.Write([]byte(`{
		  "model": "claude-opus-4-6",
		  "content": [{"type":"text","text":"Hi there"}],
		  "usage": {"input_tokens": 10, "output_tokens": 3}
		}`))
	}))
	t.Cleanup(srv.Close)

	c := anthropic.New(anthropic.Config{
		APIKey:    "test-key",
		Model:     "claude-opus-4-6",
		MaxTokens: 256,
		Endpoint:  srv.URL,
	})
	resp, err := c.Chat(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	}, llm.WithSystem("you are concise"))
	require.NoError(t, err)
	require.Equal(t, "Hi there", resp.Content)
	require.Equal(t, 10, resp.InputTokens)
	require.Equal(t, 3, resp.OutputTokens)
}

func TestStream(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start"}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":", world"}}`,
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		}
		for _, ev := range events {
			_, _ = w.Write([]byte(ev + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	c := anthropic.New(anthropic.Config{
		APIKey: "k", Model: "m", Endpoint: srv.URL,
	})

	var out strings.Builder
	for chunk := range c.Stream(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	}) {
		require.NoError(t, chunk.Err)
		out.WriteString(chunk.Content)
	}
	require.Equal(t, "Hello, world", out.String())
}

func TestEmbed_Unsupported(t *testing.T) {
	t.Parallel()
	c := anthropic.New(anthropic.Config{APIKey: "k"})
	_, err := c.Embed(context.Background(), "text")
	require.Error(t, err)
}

func TestChat_ErrorStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	t.Cleanup(srv.Close)

	c := anthropic.New(anthropic.Config{APIKey: "k", Model: "m", Endpoint: srv.URL})
	_, err := c.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

// compile-time: Client satisfies llm.Client.
var _ llm.Client = (*anthropic.Client)(nil)
