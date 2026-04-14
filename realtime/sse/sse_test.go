package sse_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/realtime/sse"
)

func newHub() *sse.Hub {
	return sse.NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestHub_publishReachesConnectedClient: the handler streams events.
// The response headers are not flushed until the first event arrives,
// so we issue the request in a goroutine and publish from the main
// test; the goroutine reads one frame and signals via the channel.
func TestHub_publishReachesConnectedClient(t *testing.T) {
	t.Parallel()
	h := newHub()
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	type result struct {
		frame string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		buf := make([]byte, 256)
		n, rerr := resp.Body.Read(buf)
		done <- result{frame: string(buf[:n]), err: rerr}
	}()

	require.Eventually(t, func() bool { return h.ClientCount() == 1 },
		time.Second, 10*time.Millisecond, "client should connect")
	h.Publish(context.Background(), sse.Event{Event: "ping", Data: "hi"})

	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.Contains(t, r.frame, "event: ping")
		require.Contains(t, r.frame, "data: hi")
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE frame")
	}
}

// TestHub_publishWithoutSubscribersIsNoop: Publish with zero clients
// must not block or panic.
func TestHub_publishWithoutSubscribersIsNoop(t *testing.T) {
	t.Parallel()
	h := newHub()
	h.Publish(context.Background(), sse.Event{Data: "x"})
	require.Equal(t, 0, h.ClientCount())
}

// TestHub_handlerRejectsNonFlusher: a ResponseWriter that doesn't
// implement http.Flusher should get a 500.
func TestHub_handlerRejectsNonFlusher(t *testing.T) {
	t.Parallel()
	h := newHub()
	rr := &nonFlushingRecorder{rec: httptest.NewRecorder()}
	h.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusInternalServerError, rr.rec.Code)
	require.Contains(t, strings.ToLower(rr.rec.Body.String()), "streaming")
}

// nonFlushingRecorder wraps httptest.ResponseRecorder *without*
// exposing Flush so the handler's non-flusher branch runs.
type nonFlushingRecorder struct {
	rec *httptest.ResponseRecorder
}

func (n *nonFlushingRecorder) Header() http.Header         { return n.rec.Header() }
func (n *nonFlushingRecorder) Write(b []byte) (int, error) { return n.rec.Write(b) }
func (n *nonFlushingRecorder) WriteHeader(code int)        { n.rec.WriteHeader(code) }
