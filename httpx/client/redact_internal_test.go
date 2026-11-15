// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/httpx/internal/secretparam"
)

type attributeCountingSpan struct {
	trace.Span
	writes int
}

func (*attributeCountingSpan) IsRecording() bool { return true }

func (s *attributeCountingSpan) SetAttributes(...attribute.KeyValue) { s.writes++ }

type requestCheckingTransport struct {
	testing *testing.T
	want    *http.Request
	span    *attributeCountingSpan
	writes  int
}

func (r *requestCheckingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.testing.Helper()
	if req != r.want {
		r.testing.Error("redacting transport replaced request")
	}
	if r.span.writes != r.writes {
		r.testing.Errorf("attribute writes before upstream = %d, want %d", r.span.writes, r.writes)
	}
	return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func TestRedactingTransportAttributeWrites(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		query  string
		span   bool
		writes int
	}{
		{name: "secret with span", query: "api_key=hidden", span: true, writes: 1},
		{name: "no secret with span", query: "q=1", span: true},
		{name: "no span", query: "api_key=hidden"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			span := &attributeCountingSpan{}
			if test.span {
				ctx = trace.ContextWithSpan(ctx, span)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/?"+test.query, http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			inner := &requestCheckingTransport{testing: t, want: req, span: span, writes: test.writes}
			transport := &redactingTransport{next: inner, secrets: secretparam.Set(secretparam.Defaults(), nil)}
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			if err = resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
