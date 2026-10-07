// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package llm_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/ai/llm"
	httpclient "github.com/golusoris/golusoris/httpx/client"
)

type deadlineTransport struct {
	deadline time.Time
	has      bool
	body     string
	status   int
}

var errStreamRead = errors.New("stream read failed")

type failingReadCloser struct {
	reader io.Reader
	err    error
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if errors.Is(err, io.EOF) {
		return n, r.err
	}
	return n, err
}

func (*failingReadCloser) Close() error { return nil }

func (d *deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	d.deadline, d.has = req.Context().Deadline()
	status := d.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(d.body)),
	}, nil
}

func TestOpenAIChatHonorsExactErrorBoundaryAndRejectsOverflow(t *testing.T) {
	t.Parallel()
	newClient := func(body string, maxBytes int64) *llm.OpenAIClient {
		return newOpenAIClient(t, llm.Config{
			BaseURL: "https://example.invalid/v1", Model: "m", MaxErrorBytes: maxBytes,
			HTTPClient: &http.Client{Transport: &deadlineTransport{
				status: http.StatusBadRequest, body: body,
			}},
		})
	}
	_, err := newClient("1234", 4).Chat(context.Background(), nil)
	if err == nil || errors.Is(err, httpclient.ErrBodyTooLarge) || !strings.Contains(err.Error(), "1234") {
		t.Fatalf("exact-boundary error = %v; want HTTP error containing body", err)
	}
	_, err = newClient("12345", 4).Chat(context.Background(), nil)
	if !errors.Is(err, httpclient.ErrBodyTooLarge) {
		t.Fatalf("overflow error = %v; want ErrBodyTooLarge", err)
	}
}

func newOpenAIClient(t *testing.T, cfg llm.Config) *llm.OpenAIClient {
	t.Helper()
	client, err := llm.NewOpenAIClient(cfg)
	if err != nil {
		t.Fatalf("NewOpenAIClient: %v", err)
	}
	return client
}

func TestNewOpenAIClientRejectsNegativeTimeout(t *testing.T) {
	t.Parallel()
	client, err := llm.NewOpenAIClient(llm.Config{Timeout: -time.Second})
	if err == nil {
		t.Fatal("negative timeout accepted")
	}
	if client != nil {
		t.Fatalf("client = %T; want nil after invalid configuration", client)
	}
}

func TestNormalizeHTTPBoundsDefaultsAndRejectsUnboundedValues(t *testing.T) {
	t.Parallel()
	bounds, err := llm.NormalizeHTTPBounds(llm.HTTPBounds{})
	if err != nil {
		t.Fatalf("NormalizeHTTPBounds defaults: %v", err)
	}
	if bounds.Timeout != llm.DefaultHTTPTimeout ||
		bounds.MaxResponseBytes != llm.DefaultMaxResponseBytes ||
		bounds.MaxErrorBytes != llm.DefaultMaxErrorBytes ||
		bounds.MaxStreamFrameBytes != llm.DefaultMaxStreamFrameBytes {
		t.Fatalf("defaults = %+v", bounds)
	}

	tests := map[string]llm.HTTPBounds{
		"negative timeout":       {Timeout: -1},
		"negative response cap":  {MaxResponseBytes: -1},
		"unbounded response cap": {MaxResponseBytes: math.MaxInt64},
		"negative error cap":     {MaxErrorBytes: -1},
		"unbounded error cap":    {MaxErrorBytes: math.MaxInt64},
		"negative frame cap":     {MaxStreamFrameBytes: -1},
		"overflowing frame cap":  {MaxStreamFrameBytes: math.MaxInt},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := llm.NormalizeHTTPBounds(input); err == nil {
				t.Fatal("invalid bounds accepted")
			}
		})
	}
}

func TestNewOpenAIClientAcceptsTypedNilHTTPClient(t *testing.T) {
	t.Parallel()
	var clientWithType *http.Client
	client, err := llm.NewOpenAIClient(llm.Config{HTTPClient: clientWithType})
	if err != nil {
		t.Fatalf("NewOpenAIClient: %v", err)
	}
	if client == nil {
		t.Fatal("client is nil")
	}
}

func TestOpenAIClientClonesInjectedUnboundedClientWithFiniteTimeout(t *testing.T) {
	t.Parallel()
	transport := &deadlineTransport{body: `{"model":"m","choices":[],"usage":{}}`}
	source := &http.Client{Transport: transport}
	client := newOpenAIClient(t, llm.Config{
		BaseURL: "https://example.invalid/v1", Model: "m",
		Timeout: time.Hour, HTTPClient: source,
	})
	_, err := client.Chat(context.Background(), nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !transport.has {
		t.Fatal("injected client request had no finite deadline")
	}
	if remaining := time.Until(transport.deadline); remaining <= 0 || remaining > time.Hour {
		t.Fatalf("deadline remaining = %v; want 0..1h", remaining)
	}
	if source.Timeout != 0 {
		t.Fatalf("caller-owned client mutated to timeout %v", source.Timeout)
	}
}

func TestOpenAIChatHonorsExactResponseBoundaryAndRejectsOverflow(t *testing.T) {
	t.Parallel()
	const payload = `{"model":"m","choices":[{"message":{"content":"ok"}}],"usage":{}}`
	newClient := func(maxBytes int64) *llm.OpenAIClient {
		return newOpenAIClient(t, llm.Config{
			BaseURL: "https://example.invalid/v1", Model: "m", MaxResponseBytes: maxBytes,
			HTTPClient: &http.Client{Transport: &deadlineTransport{body: payload}},
		})
	}
	response, err := newClient(int64(len(payload))).Chat(context.Background(), nil)
	if err != nil {
		t.Fatalf("exact-boundary Chat: %v", err)
	}
	if response.Content != "ok" {
		t.Fatalf("content = %q; want ok", response.Content)
	}
	_, err = newClient(int64(len(payload)-1)).Chat(context.Background(), nil)
	if !errors.Is(err, httpclient.ErrBodyTooLarge) {
		t.Fatalf("overflow error = %v; want ErrBodyTooLarge", err)
	}
}

func TestOpenAIEmbedRejectsResponseOverflow(t *testing.T) {
	t.Parallel()
	const payload = `{"data":[{"embedding":[0.1]}]}`
	client := newOpenAIClient(t, llm.Config{
		BaseURL: "https://example.invalid/v1", Model: "m",
		MaxResponseBytes: int64(len(payload) - 1),
		HTTPClient:       &http.Client{Transport: &deadlineTransport{body: payload}},
	})
	_, err := client.Embed(context.Background(), "input")
	if !errors.Is(err, httpclient.ErrBodyTooLarge) {
		t.Fatalf("overflow error = %v; want ErrBodyTooLarge", err)
	}
}

func TestOpenAIStreamRejectsErrorResponseOverflow(t *testing.T) {
	t.Parallel()
	client := newOpenAIClient(t, llm.Config{
		BaseURL: "https://example.invalid/v1", Model: "m", MaxErrorBytes: 4,
		HTTPClient: &http.Client{Transport: &deadlineTransport{
			status: http.StatusTooManyRequests, body: "12345",
		}},
	})
	var streamErr error
	for chunk := range client.Stream(context.Background(), nil) {
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	if !errors.Is(streamErr, httpclient.ErrBodyTooLarge) {
		t.Fatalf("stream error = %v; want ErrBodyTooLarge", streamErr)
	}
}

func TestOpenAIStreamSurfacesBodyReadError(t *testing.T) {
	t.Parallel()
	const event = "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n"
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &failingReadCloser{reader: strings.NewReader(event), err: errStreamRead},
		}, nil
	})
	client := newOpenAIClient(t, llm.Config{
		BaseURL: "https://example.invalid/v1", Model: "m",
		HTTPClient: &http.Client{Transport: transport},
	})
	var content strings.Builder
	var streamErr error
	for chunk := range client.Stream(context.Background(), nil) {
		content.WriteString(chunk.Content)
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	if content.String() != "partial" {
		t.Fatalf("content = %q; want partial", content.String())
	}
	if !errors.Is(streamErr, errStreamRead) {
		t.Fatalf("stream error = %v; want read failure", streamErr)
	}
}

func TestOpenAIStreamRejectsCleanEOFBeforeDone(t *testing.T) {
	t.Parallel()
	const event = "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n"
	client := newOpenAIClient(t, llm.Config{
		BaseURL: "https://example.invalid/v1", Model: "m",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(event)),
			}, nil
		})},
	})
	var content strings.Builder
	var streamErr error
	for chunk := range client.Stream(context.Background(), nil) {
		content.WriteString(chunk.Content)
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	if content.String() != "partial" {
		t.Fatalf("content = %q; want partial", content.String())
	}
	if !errors.Is(streamErr, llm.ErrStreamTruncated) {
		t.Fatalf("stream error = %v; want missing protocol terminator", streamErr)
	}
}

func TestOpenAIStreamSurfacesProviderErrorEvent(t *testing.T) {
	t.Parallel()
	const event = "data: {\"error\":{\"message\":\"quota exceeded\"}}\n"
	client := newOpenAIClient(t, llm.Config{
		BaseURL: "https://example.invalid/v1", Model: "m",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(event))}, nil
		})},
	})
	var streamErr error
	for chunk := range client.Stream(context.Background(), nil) {
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "quota exceeded") {
		t.Fatalf("stream error = %v; want provider error", streamErr)
	}
}

func TestOpenAIStreamHonorsExactFrameBoundaryBeyond64KiB(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("x", 70<<10)
	line := fmt.Sprintf(`data: {"choices":[{"delta":{"content":%q}}]}`, content)
	newClient := func(maxFrameBytes int) *llm.OpenAIClient {
		transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(line + "\ndata: [DONE]\n")),
			}, nil
		})
		return newOpenAIClient(t, llm.Config{
			BaseURL: "https://example.invalid/v1", Model: "m",
			MaxStreamFrameBytes: maxFrameBytes,
			HTTPClient:          &http.Client{Transport: transport},
		})
	}
	var got strings.Builder
	for chunk := range newClient(len(line)).Stream(context.Background(), nil) {
		if chunk.Err != nil {
			t.Fatalf("exact-boundary stream: %v", chunk.Err)
		}
		got.WriteString(chunk.Content)
	}
	if got.Len() != len(content) {
		t.Fatalf("content bytes = %d; want %d", got.Len(), len(content))
	}
	var streamErr error
	for chunk := range newClient(len(line)-1).Stream(context.Background(), nil) {
		if chunk.Err != nil {
			streamErr = chunk.Err
		}
	}
	if !errors.Is(streamErr, llm.ErrStreamFrameTooLarge) {
		t.Fatalf("overflow error = %v; want ErrStreamFrameTooLarge", streamErr)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
