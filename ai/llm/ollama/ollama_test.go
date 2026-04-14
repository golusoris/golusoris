package ollama_test

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
	"github.com/golusoris/golusoris/ai/llm/ollama"
)

func TestChat(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/chat", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "llama3.3", got["model"])
		require.Equal(t, false, got["stream"])

		_, _ = w.Write([]byte(`{
		  "model": "llama3.3",
		  "done": true,
		  "message": {"role":"assistant","content":"Hi there"},
		  "prompt_eval_count": 5,
		  "eval_count": 2
		}`))
	}))
	t.Cleanup(srv.Close)

	c := ollama.New(ollama.Config{BaseURL: srv.URL, Model: "llama3.3"})
	resp, err := c.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	require.NoError(t, err)
	require.Equal(t, "Hi there", resp.Content)
	require.Equal(t, 5, resp.InputTokens)
	require.Equal(t, 2, resp.OutputTokens)
}

func TestStream(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		events := []string{
			`{"message":{"content":"Hello"},"done":false}`,
			`{"message":{"content":", world"},"done":false}`,
			`{"message":{"content":""},"done":true}`,
		}
		for _, e := range events {
			_, _ = w.Write([]byte(e + "\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	c := ollama.New(ollama.Config{BaseURL: srv.URL, Model: "m"})
	var out strings.Builder
	for chunk := range c.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}) {
		require.NoError(t, chunk.Err)
		out.WriteString(chunk.Content)
	}
	require.Equal(t, "Hello, world", out.String())
}

func TestEmbed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/embeddings", r.URL.Path)
		_, _ = w.Write([]byte(`{"embedding":[0.1, 0.2, 0.3]}`))
	}))
	t.Cleanup(srv.Close)

	c := ollama.New(ollama.Config{BaseURL: srv.URL, Model: "nomic-embed-text"})
	vec, err := c.Embed(context.Background(), "hello")
	require.NoError(t, err)
	require.Len(t, vec, 3)
	require.InDelta(t, 0.2, vec[1], 1e-6)
}

var _ llm.Client = (*ollama.Client)(nil)
