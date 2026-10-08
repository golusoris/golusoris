// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/mcp"
)

// blockingHandler publishes each request and holds it open until its context
// ends or release is closed, like a streamable-HTTP stream.
func blockingHandler(seen chan<- *http.Request, release <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen <- r
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
}

// serve runs h for one request in the background and reports when it returns.
func serve(h http.Handler, method string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/mcp", http.NoBody))
	}()
	return done
}

func waitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not end within 5s", what)
	}
}

func TestEndStreamsOnShutdown(t *testing.T) {
	t.Parallel()
	srv := &http.Server{ReadHeaderTimeout: time.Second}
	seen := make(chan *http.Request, 3)
	release := make(chan struct{})
	h := mcp.EndStreamsOnShutdown(context.Background(), srv, blockingHandler(seen, release))

	streamDone := serve(h, http.MethodGet)
	callDone := serve(h, http.MethodPost)
	reqs := map[string]*http.Request{}
	for range 2 {
		r := <-seen
		reqs[r.Method] = r
	}
	stream, call := reqs[http.MethodGet].Context(), reqs[http.MethodPost].Context()
	if stream.Err() != nil || call.Err() != nil {
		t.Fatalf("requests ended before shutdown: stream=%v call=%v", stream.Err(), call.Err())
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	waitDone(t, streamDone, "GET stream after shutdown began")
	// Cancellation would run on its own goroutine, so allow it time to land.
	select {
	case <-call.Done():
		t.Fatalf("POST request was cancelled by shutdown: %v", call.Err())
	case <-time.After(200 * time.Millisecond):
	}

	waitDone(t, serve(h, http.MethodGet), "GET stream opened after shutdown")
	<-seen
	close(release)
	waitDone(t, callDone, "POST request after release")
}
