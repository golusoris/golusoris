// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
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

var errBoom = errors.New("boom")

// roundTripFunc adapts a func into an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// faultyBody serves payload, then reports readErr (when set) instead of
// io.EOF, and reports closeErr (when set) from Close.
type faultyBody struct {
	r        io.Reader
	readErr  error
	closeErr error
}

func (b *faultyBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if errors.Is(err, io.EOF) && b.readErr != nil {
		return n, b.readErr
	}
	return n, err
}

func (b *faultyBody) Close() error { return b.closeErr }

// faultyClient answers every request with 200 and a faultyBody.
func faultyClient(payload string, readErr, closeErr error) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &faultyBody{r: strings.NewReader(payload), readErr: readErr, closeErr: closeErr},
		}, nil
	})}
}

// drain consumes a Stream channel and returns the concatenated content
// plus the single error chunk, if any.
func drain(t *testing.T, ch <-chan llm.Chunk) (string, error) {
	t.Helper()
	var sb strings.Builder
	var streamErr error
	for chunk := range ch {
		if chunk.Err != nil {
			require.NoError(t, streamErr, "more than one error chunk")
			streamErr = chunk.Err
			continue
		}
		sb.WriteString(chunk.Content)
	}
	return sb.String(), streamErr
}

const deltaLine = `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}` + "\n"

func streamHi(t *testing.T, hc *http.Client) (string, error) {
	t.Helper()
	c := anthropic.New(anthropic.Config{APIKey: "k", Model: "m", HTTPClient: hc})
	return drain(t, c.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}))
}

// Negative: a body read error after partial content ends the stream
// with an error chunk instead of a clean close.
func TestStream_readErrorSurfacesErrChunk(t *testing.T) {
	t.Parallel()
	out, err := streamHi(t, faultyClient(deltaLine, errBoom, nil))
	require.Equal(t, "Hello", out)
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "anthropic: stream")
}

// Negative: a failed body close surfaces as the error chunk.
func TestStream_closeErrorSurfacesErrChunk(t *testing.T) {
	t.Parallel()
	out, err := streamHi(t, faultyClient(deltaLine, nil, errBoom))
	require.Equal(t, "Hello", out)
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "close stream body")
}

// Boundary: when both fail, the read error is primary and the close
// error is not joined onto it.
func TestStream_readErrorWinsOverCloseError(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("close failed")
	_, err := streamHi(t, faultyClient(deltaLine, errBoom, closeErr))
	require.ErrorIs(t, err, errBoom)
	require.False(t, errors.Is(err, closeErr))
}

// Negative: Chat reports a failed body close even after a good decode.
func TestChat_closeErrorReturnsErr(t *testing.T) {
	t.Parallel()
	const payload = `{"model":"m","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`
	c := anthropic.New(anthropic.Config{APIKey: "k", Model: "m", HTTPClient: faultyClient(payload, nil, errBoom)})
	_, err := c.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "close response body")
}
