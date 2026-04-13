package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/ai/llm"
)

func fakeOpenAI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"data": []map[string]any{
					{"embedding": []float32{0.1, 0.2, 0.3}},
				},
			})
			return
		}
		// Check for streaming.
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if stream, _ := req["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\" World\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"model": "test-model",
			"choices": []map[string]any{
				{"message": map[string]string{"content": "pong"}},
			},
			"usage": map[string]int{
				"prompt_tokens": 10, "completion_tokens": 1,
			},
		})
	}))
}

func TestChat(t *testing.T) {
	srv := fakeOpenAI(t)
	defer srv.Close()

	client := llm.NewOpenAIClient(llm.Config{BaseURL: srv.URL, Model: "test"})
	resp, err := client.Chat(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "ping"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "pong" {
		t.Fatalf("expected 'pong', got %q", resp.Content)
	}
	if resp.InputTokens != 10 {
		t.Fatalf("expected 10 input tokens, got %d", resp.InputTokens)
	}
}

func TestStream(t *testing.T) {
	srv := fakeOpenAI(t)
	defer srv.Close()

	client := llm.NewOpenAIClient(llm.Config{BaseURL: srv.URL, Model: "test"})
	ch := client.Stream(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	})

	var got strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		got.WriteString(chunk.Content)
	}
	if got.String() != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", got.String())
	}
}

func TestEmbed(t *testing.T) {
	srv := fakeOpenAI(t)
	defer srv.Close()

	client := llm.NewOpenAIClient(llm.Config{BaseURL: srv.URL, Model: "embed-model"})
	vec, err := client.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3 dims, got %d", len(vec))
	}
}

func TestOptions(t *testing.T) {
	srv := fakeOpenAI(t)
	defer srv.Close()

	client := llm.NewOpenAIClient(llm.Config{BaseURL: srv.URL, Model: "default"})
	_, err := client.Chat(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		llm.WithModel("override"),
		llm.WithMaxTokens(100),
		llm.WithTemperature(0.5),
		llm.WithSystem("You are helpful."),
	)
	if err != nil {
		t.Fatal(err)
	}
}
