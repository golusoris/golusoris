// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/golusoris/golusoris/httpx/client"
)

type urlTraceCase struct {
	name     string
	query    string
	want     string
	extra    []string
	userinfo bool
}

func TestURLFullRedactsSecrets(t *testing.T) {
	t.Parallel()
	for _, test := range []urlTraceCase{
		{name: "positive", query: "api_key=s3cr3t&q=1", want: "api_key=REDACTED&q=1"},
		{name: "negative", query: "z=2&q=a%20b&q=%2f", want: "z=2&q=a%20b&q=%2f"},
		{name: "no query"},
		{name: "mixed case", query: "API_KEY=hidden&q=1", want: "API_KEY=REDACTED&q=1"},
		{name: "repeated and empty", query: "token=first&token=second&token=&q=1", want: "token=REDACTED&token=REDACTED&token=REDACTED&q=1"},
		{name: "bare name", query: "api-key&q=1", want: "api-key=REDACTED&q=1"},
		{name: "extra keeps defaults", query: "My_Secret=extra&api_key=builtin&q=1", want: "My_Secret=REDACTED&api_key=REDACTED&q=1", extra: []string{"my-secret"}},
		{name: "encoded name", query: "api%5Fkey=hidden&q=1", want: "api%5Fkey=REDACTED&q=1"},
		{name: "malformed value", query: "api_key=hidden%zz&q=1", want: "api_key=REDACTED&q=1"},
		{name: "semicolon value", query: "api_key=hidden;more&q=1", want: "api_key=REDACTED&q=1"},
		{name: "userinfo", query: "api_key=hidden&q=1", want: "api_key=REDACTED&q=1", userinfo: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkURLTrace(t, test)
		})
	}
}

func checkURLTrace(t *testing.T, test urlTraceCase) {
	t.Helper()
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		received <- req.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	t.Cleanup(func() { shutdownTraceProvider(t, provider) })
	c := client.New(client.Options{TracerProvider: provider, SecretParams: test.extra})
	t.Cleanup(c.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req := newURLTraceRequest(t, ctx, server.URL, test)
	original := req.URL.String()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err = resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := req.URL.String(); got != original {
		t.Fatalf("request URL = %q, want unchanged %q", got, original)
	}
	select {
	case got := <-received:
		if got != test.query {
			t.Fatalf("upstream query = %q, want %q", got, test.query)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	want := server.URL + "/"
	if test.want != "" {
		want += "?" + test.want
	}
	checkURLFull(t, exporter, want)
}

func newURLTraceRequest(t *testing.T, ctx context.Context, base string, test urlTraceCase) *http.Request {
	t.Helper()
	target := base + "/"
	if test.query != "" {
		target += "?" + test.query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if test.userinfo {
		req.URL.User = url.UserPassword("trace-user", "trace-password")
	}
	return req
}

func checkURLFull(t *testing.T, exporter *tracetest.InMemoryExporter, want string) {
	t.Helper()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	for _, attr := range spans[0].Attributes {
		if attr.Key == "url.full" {
			if got := attr.Value.AsString(); got != want {
				t.Fatalf("url.full = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatal("url.full missing")
}

func shutdownTraceProvider(t *testing.T, provider *trace.TracerProvider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		t.Error(err)
	}
}
