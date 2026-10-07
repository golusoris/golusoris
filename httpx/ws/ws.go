// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ws is a thin wrapper over coder/websocket. It exposes:
//
//   - [Accept] that applies an origin-check + sensible defaults.
//   - [Broadcaster], a reference fan-out helper for single-process pub/sub
//     over a set of connections.
//
// Apps build their own room/hub logic on top — one-size-fits-all hubs are
// always wrong. When pub/sub must span replicas, wire a
// [realtime/pubsub]-backed broadcaster.
package ws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/golusoris/golusoris/httpx/middleware"
)

// AcceptOptions tunes the handshake.
type AcceptOptions struct {
	// AllowedOrigins lists exact http(s) origins permitted to originate the WS
	// request, including a non-default port when applicable. Empty means exact
	// same-origin only. A literal "*" disables the check; use it only for
	// genuinely public APIs.
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
	source, err := parseWebOrigin(origin)
	if err != nil {
		return false
	}
	target, err := requestWebOrigin(r)
	if err != nil {
		return false
	}
	if source == target {
		return true
	}
	for _, candidate := range allowed {
		parsed, parseErr := parseWebOrigin(candidate)
		if parseErr == nil && parsed == source {
			return true
		}
	}
	return false
}

type webOrigin struct {
	scheme string
	host   string
	port   uint16
}

func parseWebOrigin(raw string) (webOrigin, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return webOrigin{}, fmt.Errorf("parse origin: %w", err)
	}
	if parsed.User != nil || parsed.Host == "" || parsed.Path != "" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || parsed.Opaque != "" {
		return webOrigin{}, errors.New("origin must contain only scheme and authority")
	}
	return canonicalWebOrigin(parsed.Scheme, parsed)
}

func requestWebOrigin(r *http.Request) (webOrigin, error) {
	scheme := middleware.ForwardedProtoFromContext(r.Context())
	if scheme == "" {
		scheme = "http"
		if r.TLS != nil {
			scheme = "https"
		}
	}
	parsed, err := url.Parse("//" + r.Host)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" {
		return webOrigin{}, errors.New("request host is not a valid authority")
	}
	return canonicalWebOrigin(scheme, parsed)
}

func canonicalWebOrigin(scheme string, parsed *url.URL) (webOrigin, error) {
	scheme = strings.ToLower(scheme)
	if scheme != "http" && scheme != "https" {
		return webOrigin{}, fmt.Errorf("unsupported origin scheme %q", scheme)
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" || strings.ContainsAny(host, "%/\\ \t\r\n") {
		return webOrigin{}, errors.New("origin host is invalid")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port, err := originPort(scheme, parsed.Port())
	if err != nil {
		return webOrigin{}, err
	}
	return webOrigin{scheme: scheme, host: host, port: port}, nil
}

func originPort(scheme, raw string) (uint16, error) {
	if raw == "" {
		if scheme == "https" {
			return 443, nil
		}
		return 80, nil
	}
	port, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("origin port %q is invalid", raw)
	}
	return uint16(port), nil
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
	done := make(chan struct{})
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	unsubOnce := sync.Once{}
	unsub := func() {
		unsubOnce.Do(func() {
			close(done)
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
	go watchSubscription(ctx, done, unsub)
	return ch, unsub
}

func watchSubscription(ctx context.Context, done <-chan struct{}, unsub func()) {
	select {
	case <-ctx.Done():
		unsub()
	case <-done:
	}
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
