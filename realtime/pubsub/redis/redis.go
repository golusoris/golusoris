// Package redis provides a cross-replica pub/sub Bus backed by Redis
// PUBLISH/SUBSCRIBE (rueidis), implementing [pubsub.Bus]. Use it in place of
// the in-process pubsub.LocalBus when messages must reach subscribers on other
// replicas.
//
//	fx.New(golusoris.Core, golusoris.CacheRedis, pubsubredis.Module)
//
// Message.Data is encoded for the wire: []byte and string pass through, any
// other value is JSON-marshalled. Subscribers receive Data as the raw []byte
// payload (decode as needed).
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/realtime/pubsub"
)

// Bus is a cross-replica pub/sub bus backed by Redis pub/sub.
type Bus struct {
	client rueidis.Client
	logger *slog.Logger
}

var _ pubsub.Bus = (*Bus)(nil)

// New returns a Redis-backed pub/sub Bus.
func New(client rueidis.Client, logger *slog.Logger) *Bus {
	return &Bus{client: client, logger: logger}
}

// Publish encodes msg.Data and PUBLISHes it to the msg.Topic channel. Errors
// are logged — the [pubsub.Bus] contract is fire-and-forget.
func (b *Bus) Publish(ctx context.Context, msg pubsub.Message) {
	payload, err := encode(msg.Data)
	if err != nil {
		b.logger.ErrorContext(ctx, "pubsub/redis: encode", slog.String("topic", msg.Topic), slog.Any("err", err))
		return
	}
	if err := b.client.Do(ctx, b.client.B().Publish().Channel(msg.Topic).Message(payload).Build()).Error(); err != nil {
		b.logger.ErrorContext(ctx, "pubsub/redis: publish", slog.String("topic", msg.Topic), slog.Any("err", err))
	}
}

// Subscribe SUBSCRIBEs to topic on a dedicated connection in a background
// goroutine. The returned func cancels the subscription.
func (b *Bus) Subscribe(topic string, h pubsub.Handler) func() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err := b.client.Receive(ctx, b.client.B().Subscribe().Channel(topic).Build(), func(m rueidis.PubSubMessage) {
			h(pubsub.Message{Topic: m.Channel, Data: []byte(m.Message)})
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			b.logger.ErrorContext(ctx, "pubsub/redis: subscription ended", slog.String("topic", topic), slog.Any("err", err))
		}
	}()
	return cancel
}

func encode(data any) (string, error) {
	switch v := data.(type) {
	case nil:
		return "", nil
	case []byte:
		return string(v), nil
	case string:
		return v, nil
	default:
		out, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("pubsub/redis: marshal data: %w", err)
		}
		return string(out), nil
	}
}

// Module provides a [pubsub.Bus] backed by Redis. Requires a rueidis.Client
// (golusoris.CacheRedis) and [Core] for the logger.
var Module = fx.Module("golusoris.realtime.pubsub.redis",
	fx.Provide(func(c rueidis.Client, logger *slog.Logger) pubsub.Bus {
		return New(c, logger)
	}),
)
