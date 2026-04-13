// Package ws is a thin wrapper over coder/websocket. It exposes:
//
//   - [Accept] that applies an origin-check + sensible defaults.
//   - [Broadcaster], a reference fan-out helper for single-process pub/sub
//     over a set of connections.
//
// Apps build their own room/hub logic on top — one-size-fits-all hubs are
// always wrong. When pub/sub must span replicas, wire a
// [realtime/pubsub]-backed broadcaster (Step 10).
package ws

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// AcceptOptions tunes the handshake.
type AcceptOptions struct {
	// AllowedOrigins lists hostnames permitted to originate the WS request.
	// Empty means accept only same-origin (r.Host). Includes a literal "*"
	// entry to disable the check entirely — use only for public APIs.
	AllowedOrigins []string
	// Subprotocols is forwarded to websocket.AcceptOptions.
	Subprotocols []string
	// CompressionMode mirrors websocket.CompressionMode. Default is
	// CompressionDisabled.
	CompressionMode websocket.CompressionMode
}

// Accept upgrades the HTTP connection to a WebSocket with an origin check
// enforced before the upgrade. Returns the same *websocket.Conn type
// coder/websocket users already know.
func Accept(w http.ResponseWriter, r *http.Request, opts AcceptOptions) (*websocket.Conn, error) {
	if !originAllowed(r, opts.AllowedOrigins) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return nil, errors.New("ws: origin not allowed")
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: opts.Subprotocols,
		// We check origin ourselves — tell coder/websocket to allow all so
		// it doesn't second-guess us.
		InsecureSkipVerify: true,
		CompressionMode:    opts.CompressionMode,
	})
	if err != nil {
		return nil, fmt.Errorf("ws: accept: %w", err)
	}
	return c, nil
}

// originAllowed implements same-origin-by-default + explicit allowlist.
func originAllowed(r *http.Request, allowed []string) bool {
	if slices.Contains(allowed, "*") {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients may not send Origin; accept.
		return true
	}
	// Same-origin check: strip scheme + port from Origin and compare to Host.
	host := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	hostNoPort := host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		hostNoPort = host[:i]
	}
	reqHost := r.Host
	if i := strings.IndexByte(reqHost, ':'); i >= 0 {
		reqHost = reqHost[:i]
	}
	if hostNoPort == reqHost {
		return true
	}
	return slices.Contains(allowed, host) || slices.Contains(allowed, hostNoPort)
}

// Broadcaster fans out messages to a set of in-process subscribers. Each
// Subscribe returns a channel that the caller reads from; Publish sends to
// all live subscribers without blocking slow ones (messages to a full
// buffer are dropped for that subscriber).
//
// Suitable for a single process. For multi-replica deployments, layer a
// pubsub backend (pg LISTEN/NOTIFY, redis, NATS — see realtime/pubsub).
type Broadcaster[T any] struct {
	mu      sync.RWMutex
	subs    map[chan T]struct{}
	bufSize int
}

// NewBroadcaster returns a broadcaster with the given per-subscriber buffer.
func NewBroadcaster[T any](bufSize int) *Broadcaster[T] {
	if bufSize <= 0 {
		bufSize = 16
	}
	return &Broadcaster[T]{subs: make(map[chan T]struct{}), bufSize: bufSize}
}

// Subscribe returns a receive-only channel + an unsubscribe func that
// removes + closes the channel. ctx cancellation is honored: when ctx is
// done, the subscription is automatically torn down.
func (b *Broadcaster[T]) Subscribe(ctx context.Context) (<-chan T, func()) {
	ch := make(chan T, b.bufSize)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	unsubOnce := sync.Once{}
	unsub := func() {
		unsubOnce.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
	go func() {
		<-ctx.Done()
		unsub()
	}()
	return ch, unsub
}

// Publish sends msg to every subscriber. Slow subscribers whose buffer is
// full have this message dropped; their subscription is not torn down (let
// callers decide policy).
func (b *Broadcaster[T]) Publish(msg T) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
			// Subscriber is slow; drop this message.
		}
	}
}

// Count returns the current number of subscribers — useful for metrics.
func (b *Broadcaster[T]) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
