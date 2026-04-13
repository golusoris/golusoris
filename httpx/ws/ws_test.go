package ws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	cws "github.com/coder/websocket"

	"github.com/golusoris/golusoris/httpx/ws"
)

func TestAcceptSameOrigin(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Accept(w, r, ws.AcceptOptions{})
		if err != nil {
			return
		}
		_ = c.Close(cws.StatusNormalClosure, "bye")
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	c, resp, err := cws.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	_ = c.CloseNow()
}

func TestAcceptRejectsCrossOrigin(t *testing.T) {
	t.Parallel()
	var rejected atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := ws.Accept(w, r, ws.AcceptOptions{})
		if err != nil {
			rejected.Store(true)
		}
	}))
	defer srv.Close()

	// Manual HTTP request with a spoofed cross-site Origin — dial won't
	// succeed but we want to assert the handler refused.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if !rejected.Load() {
		t.Error("Accept did not report rejection")
	}
}

func TestBroadcasterFansOut(t *testing.T) {
	t.Parallel()
	bc := ws.NewBroadcaster[int](4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1, _ := bc.Subscribe(ctx)
	ch2, _ := bc.Subscribe(ctx)
	if bc.Count() != 2 {
		t.Errorf("Count = %d", bc.Count())
	}

	bc.Publish(1)
	bc.Publish(2)

	want := []int{1, 2}
	for i, ch := range []<-chan int{ch1, ch2} {
		for _, w := range want {
			select {
			case got := <-ch:
				if got != w {
					t.Errorf("subscriber %d: got %d, want %d", i, got, w)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("subscriber %d: timed out waiting for %d", i, w)
			}
		}
	}
}

func TestBroadcasterUnsubscribeOnContextCancel(t *testing.T) {
	t.Parallel()
	bc := ws.NewBroadcaster[int](4)
	ctx, cancel := context.WithCancel(context.Background())
	_, _ = bc.Subscribe(ctx)
	if bc.Count() != 1 {
		t.Fatalf("Count = %d", bc.Count())
	}
	cancel()
	// Goroutine-driven cleanup; wait a tick.
	for range 20 {
		if bc.Count() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("Count still %d after cancel", bc.Count())
}
