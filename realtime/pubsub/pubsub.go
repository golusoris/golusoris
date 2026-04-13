// Package pubsub provides a lightweight in-process pub/sub bus. For
// cross-replica pub/sub use the redis sub-package (backed by rueidis
// SUBSCRIBE) or the pg sub-package (backed by LISTEN/NOTIFY) — both
// implement the same [Bus] interface.
//
// Usage:
//
//	bus := pubsub.New()
//
//	unsub := bus.Subscribe("order.created", func(msg pubsub.Message) {
//	    fmt.Println(msg.Topic, msg.Data)
//	})
//	defer unsub()
//
//	bus.Publish(ctx, pubsub.Message{Topic: "order.created", Data: payload})
package pubsub

import (
	"context"
	"sync"
)

// Message is the envelope passed to subscribers.
type Message struct {
	Topic string
	Data  any
}

// Handler is a function called for each published message.
type Handler func(msg Message)

// Bus is the pub/sub abstraction. The in-process implementation is
// [*LocalBus]. Cross-replica backends implement the same interface.
type Bus interface {
	// Publish sends msg to all subscribers of msg.Topic.
	Publish(ctx context.Context, msg Message)
	// Subscribe registers h for topic. Returns a cancel func to unsubscribe.
	Subscribe(topic string, h Handler) (cancel func())
}

// LocalBus is an in-process, goroutine-safe pub/sub bus. Suitable for
// single-replica apps or as a dev fallback.
type LocalBus struct {
	mu   sync.RWMutex
	subs map[string]map[uint64]Handler
	seq  uint64
}

// New returns an initialised LocalBus.
func New() *LocalBus {
	return &LocalBus{subs: make(map[string]map[uint64]Handler)}
}

// Subscribe registers h for topic. The returned func removes the
// subscription when called.
func (b *LocalBus) Subscribe(topic string, h Handler) func() {
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]Handler)
	}
	b.seq++
	id := b.seq
	b.subs[topic][id] = h
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		delete(b.subs[topic], id)
		if len(b.subs[topic]) == 0 {
			delete(b.subs, topic)
		}
		b.mu.Unlock()
	}
}

// Publish calls all handlers registered for msg.Topic synchronously
// in the calling goroutine. Handlers must not block.
func (b *LocalBus) Publish(_ context.Context, msg Message) {
	b.mu.RLock()
	handlers := b.subs[msg.Topic]
	b.mu.RUnlock()
	for _, h := range handlers {
		h(msg)
	}
}
