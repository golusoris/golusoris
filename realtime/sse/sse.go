// Package sse provides a Server-Sent Events (SSE) handler. Clients
// connect over HTTP and receive a stream of events. The server pushes
// events via a per-connection channel.
//
// Usage:
//
//	hub := sse.NewHub()
//	go hub.Run(ctx) // starts the event loop
//
//	// Mount the SSE endpoint:
//	mux.Handle("/events", hub.Handler())
//
//	// Publish from anywhere:
//	hub.Publish(ctx, sse.Event{Event: "order.updated", Data: payload})
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

// Event is a single SSE message.
type Event struct {
	// ID is the optional event ID (sets "Last-Event-ID" on the client).
	ID string
	// Event is the event type (maps to `event:` field). Default "message".
	Event string
	// Data is the payload. Structs are JSON-encoded automatically.
	Data any
	// Retry is the reconnect delay in ms (optional).
	Retry int
}

// format serialises e as an SSE wire frame.
func (e Event) format() ([]byte, error) {
	var buf []byte
	if e.ID != "" {
		buf = append(buf, fmt.Sprintf("id: %s\n", e.ID)...)
	}
	name := e.Event
	if name == "" {
		name = "message"
	}
	buf = append(buf, fmt.Sprintf("event: %s\n", name)...)
	if e.Retry > 0 {
		buf = append(buf, fmt.Sprintf("retry: %d\n", e.Retry)...)
	}
	var data string
	switch v := e.Data.(type) {
	case string:
		data = v
	case []byte:
		data = string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("sse: marshal data: %w", err)
		}
		data = string(b)
	}
	buf = append(buf, fmt.Sprintf("data: %s\n\n", data)...)
	return buf, nil
}

// client is a connected SSE subscriber.
type client struct {
	ch     chan Event
	closed chan struct{}
}

// Hub manages connected SSE clients and broadcasts events.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	logger  *slog.Logger
	bufSize int
}

// NewHub returns a Hub. Use [Hub.Handler] to get the HTTP handler and
// [Hub.Publish] to push events.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
		logger:  logger,
		bufSize: 16,
	}
}

// Handler returns an http.Handler that upgrades the connection to SSE
// and streams events to the client until it disconnects.
func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

		c := &client{
			ch:     make(chan Event, h.bufSize),
			closed: make(chan struct{}),
		}
		h.add(c)
		defer h.remove(c)

		for {
			select {
			case <-r.Context().Done():
				return
			case ev, ok := <-c.ch:
				if !ok {
					return
				}
				b, err := ev.format()
				if err != nil {
					h.logger.Warn("sse: format event", slog.String("error", err.Error()))
					continue
				}
				if _, writeErr := w.Write(b); writeErr != nil {
					return
				}
				fl.Flush()
			}
		}
	})
}

// Publish sends ev to all currently connected clients. Slow clients
// that can't keep up (full buffer) are skipped non-blocking.
func (h *Hub) Publish(_ context.Context, ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.ch <- ev:
		default:
			h.logger.Debug("sse: client buffer full, dropping event")
		}
	}
}

// ClientCount returns the number of connected SSE clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	h.logger.Debug("sse: client connected", slog.Int("total", h.ClientCount()))
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.ch)
	h.logger.Debug("sse: client disconnected", slog.Int("total", h.ClientCount()))
}
