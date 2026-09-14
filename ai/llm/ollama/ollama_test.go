// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ollama_test

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

const partialLine = `{"message":{"content":"Hello"},"done":false}` + "\n"

func streamHi(t *testing.T, hc *http.Client) (string, error) {
	t.Helper()
	c := ollama.New(ollama.Config{Model: "m", HTTPClient: hc})
	return drain(t, c.Stream(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}))
}

// Negative: a body read error before {"done":true} ends the stream with
// an error chunk instead of a clean close.
func TestStream_readErrorSurfacesErrChunk(t *testing.T) {
	t.Parallel()
	out, err := streamHi(t, faultyClient(partialLine, errBoom, nil))
	require.Equal(t, "Hello", out)
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "ollama: stream")
}

// Negative: a failed body close surfaces as the error chunk.
func TestStream_closeErrorSurfacesErrChunk(t *testing.T) {
	t.Parallel()
	out, err := streamHi(t, faultyClient(partialLine, nil, errBoom))
	require.Equal(t, "Hello", out)
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "close stream body")
}

// Boundary: when both fail, the read error is primary and the close
// error is not joined onto it.
func TestStream_readErrorWinsOverCloseError(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("close failed")
	_, err := streamHi(t, faultyClient(partialLine, errBoom, closeErr))
	require.ErrorIs(t, err, errBoom)
	require.False(t, errors.Is(err, closeErr))
}

// Negative: Chat reports a failed body close even after a good decode.
func TestChat_closeErrorReturnsErr(t *testing.T) {
	t.Parallel()
	const payload = `{"model":"m","done":true,"message":{"role":"assistant","content":"Hi"}}`
	c := ollama.New(ollama.Config{Model: "m", HTTPClient: faultyClient(payload, nil, errBoom)})
	_, err := c.Chat(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "close response body")
}

// Negative: Embed reports a failed body close even after a good decode.
func TestEmbed_closeErrorReturnsErr(t *testing.T) {
	t.Parallel()
	c := ollama.New(ollama.Config{Model: "m", HTTPClient: faultyClient(`{"embedding":[0.1]}`, nil, errBoom)})
	_, err := c.Embed(context.Background(), "hello")
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "close embed body")
}
