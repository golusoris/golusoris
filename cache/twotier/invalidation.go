// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/realtime/pubsub"
)

// DefaultInvalidationTopic is the pub/sub topic invalidations travel on.
const DefaultInvalidationTopic = "golusoris.cache.twotier.invalidate"

// InvalidationKind names what a peer replica evicts from its L1.
type InvalidationKind string

const (
	// InvalidationKey evicts one composed key.
	InvalidationKey InvalidationKind = "key"
	// InvalidationPrefix evicts every composed key that starts with Key.
	InvalidationPrefix InvalidationKind = "prefix"
)

// Invalidation is one cross-replica L1 eviction notice.
type Invalidation struct {
	// Origin identifies the sending [TwoTier]; receivers ignore their own.
	Origin string `json:"origin"`
	// Kind selects key or prefix eviction.
	Kind InvalidationKind `json:"kind"`
	// Key is the composed key, or the composed prefix for [InvalidationPrefix].
	Key string `json:"key"`
}

// Broadcaster carries invalidations between replicas. Delivery is at most
// once: a lost notice leaves a peer's L1 entry stale until its L1 TTL.
type Broadcaster interface {
	// Broadcast sends inv to every subscribed replica.
	Broadcast(ctx context.Context, inv Invalidation) error
	// Subscribe calls handle for every received notice until cancel is called.
	Subscribe(handle func(Invalidation)) (cancel func())
}

// ErrBroadcast wraps a failed invalidation broadcast. The local tiers have
// already changed; peers may serve the old value until their L1 TTL.
var ErrBroadcast = errors.New("cache/twotier: invalidation broadcast failed")

// BusBroadcaster is a [Broadcaster] over a realtime/pubsub [pubsub.Bus]; with
// realtime/pubsub/redis it shares the Redis server already used as L2.
type BusBroadcaster struct {
	bus    pubsub.Bus
	topic  string
	logger *slog.Logger
}

var _ Broadcaster = (*BusBroadcaster)(nil)

// NewBusBroadcaster returns a BusBroadcaster on topic (empty uses
// [DefaultInvalidationTopic]). A nil logger discards malformed-notice logs.
func NewBusBroadcaster(bus pubsub.Bus, topic string, logger *slog.Logger) (*BusBroadcaster, error) {
	if validate.IsNil(bus) {
		return nil, fmt.Errorf("%w: pub/sub bus", errInvalidDependency)
	}
	if topic == "" {
		topic = DefaultInvalidationTopic
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &BusBroadcaster{bus: bus, topic: topic, logger: logger}, nil
}

// Broadcast publishes inv as JSON. A bus implementing [pubsub.CheckedBus]
// reports publish failures; a plain Bus is fire-and-forget.
func (b *BusBroadcaster) Broadcast(ctx context.Context, inv Invalidation) error {
	raw, err := json.Marshal(inv)
	if err != nil {
		return fmt.Errorf("cache/twotier: encode invalidation: %w", err)
	}
	msg := pubsub.Message{Topic: b.topic, Data: raw}
	checked, ok := b.bus.(pubsub.CheckedBus)
	if !ok {
		b.bus.Publish(ctx, msg)
		return nil
	}
	if err := checked.TryPublish(ctx, msg); err != nil {
		return fmt.Errorf("cache/twotier: publish invalidation: %w", err)
	}
	return nil
}

// Subscribe decodes every notice on the topic; malformed ones are logged and
// dropped like a lost message.
func (b *BusBroadcaster) Subscribe(handle func(Invalidation)) func() {
	if handle == nil {
		return func() {}
	}
	return b.bus.Subscribe(b.topic, func(msg pubsub.Message) {
		inv, err := decodeInvalidation(msg.Data)
		if err != nil {
			b.logger.Warn("cache/twotier: drop malformed invalidation", slog.Any("error", err))
			return
		}
		handle(inv)
	})
}

func decodeInvalidation(data any) (Invalidation, error) {
	var raw []byte
	switch payload := data.(type) {
	case []byte:
		raw = payload
	case string:
		raw = []byte(payload)
	default:
		return Invalidation{}, fmt.Errorf("unsupported payload %T", data)
	}
	var inv Invalidation
	if err := json.Unmarshal(raw, &inv); err != nil {
		return Invalidation{}, fmt.Errorf("decode invalidation: %w", err)
	}
	if inv.Origin == "" || (inv.Kind != InvalidationKey && inv.Kind != InvalidationPrefix) {
		return Invalidation{}, fmt.Errorf("incomplete invalidation %+v", inv)
	}
	return inv, nil
}
