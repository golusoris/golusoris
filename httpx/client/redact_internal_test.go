// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
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
		name        string
		query       string
		fragment    string
		rawFragment string
		opaque      string
		span        bool
		writes      int
	}{
		{name: "secret with span", query: "api_key=hidden", span: true, writes: 1},
		{name: "no secret with span", query: "q=1", span: true},
		{name: "fragment without query", fragment: "token=x", span: true, writes: 1},
		{name: "raw fragment without query", rawFragment: "token=x", span: true, writes: 1},
		{name: "opaque without raw query", opaque: "//example.test/?api_key=hidden", span: true, writes: 1},
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
			req.URL.Fragment = test.fragment
			req.URL.RawFragment = test.rawFragment
			req.URL.Opaque = test.opaque
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

func TestRedactedURLNoSecretAllocations(t *testing.T) { //nolint:paralleltest // WHY: testing.AllocsPerRun forbids execution during parallel tests.
	secrets := secretparam.Set(secretparam.Defaults(), nil)
	for _, query := range []string{"q=1&a=2", "Q=1;not_a_secret=2", "%71=1&not%2Dsecret=2", "q=1&&;", "api%zzkey=x", "%=x", "%C3%84=1", ""} {
		target := &url.URL{Scheme: "http", Host: "example.test", RawQuery: query}
		allocations := testing.AllocsPerRun(100, func() {
			if result, changed := redactedURL(target, secrets); changed || result != "" {
				t.Fatalf("unexpected redaction: %q", result)
			}
		})
		if allocations != 0 {
			t.Errorf("query %q: allocations = %v, want 0", query, allocations)
		}
	}
}

func TestRedactedURLOpaqueAndRawQuery(t *testing.T) {
	t.Parallel()
	const redacted = "http://example.test/?token=REDACTED"
	for _, test := range []struct {
		target url.URL
		want   string
	}{
		{url.URL{
			Scheme: "http", Opaque: "//example.test/?token=hidden#secret=fragment",
			RawQuery: "api_key=second", Fragment: "third", RawFragment: "%74hird",
		}, redacted},
		{url.URL{Scheme: "http", Opaque: "//example.test/?token=prefix", RawQuery: "private-suffix&q=1"}, redacted + "&q=1"},
		{url.URL{Scheme: "http", Opaque: "//example.test/?token=prefix", ForceQuery: true}, redacted},
	} {
		original := test.target
		got, changed := redactedURL(&test.target, secretparam.Set(secretparam.Defaults(), nil))
		if !changed || got != test.want {
			t.Fatalf("redacted URL = %q, changed = %v, want %q", got, changed, test.want)
		}
		if test.target != original {
			t.Fatal("redactedURL mutated its input")
		}
	}
}

func TestRedactedURLOpaqueWithoutSecret(t *testing.T) {
	t.Parallel()
	target := &url.URL{Scheme: "http", Opaque: "//example.test/?q=prefix", RawQuery: "suffix"}
	if got, changed := redactedURL(target, secretparam.Set(secretparam.Defaults(), nil)); changed || got != "" {
		t.Fatalf("redacted URL = %q, changed = %v, want unchanged", got, changed)
	}
}
