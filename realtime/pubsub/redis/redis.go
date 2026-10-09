// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/retry"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/realtime/pubsub"
)

const (
	// maxSubscribeSessions bounds reconnects per subscription (HISS-02); with
	// backoff capped at 30s it outlasts any realistic process lifetime.
	maxSubscribeSessions = 1 << 20
	// subscribeTimeout bounds one SUBSCRIBE round trip.
	subscribeTimeout = 10 * time.Second
)

// errHooksStopped reports a dedicated connection that stopped delivering
// without naming a cause.
var errHooksStopped = errors.New("pubsub/redis: subscription hooks stopped")

// Bus is a cross-replica pub/sub bus backed by Redis pub/sub.
type Bus struct {
	client rueidis.Client
	logger *slog.Logger
	clk    clock.Clock
}

var (
	_ pubsub.CheckedBus = (*Bus)(nil)
	_ pubsub.GapBus     = (*Bus)(nil)
)

// New returns a Redis-backed pub/sub Bus.
func New(client rueidis.Client, logger *slog.Logger) *Bus {
	if validate.IsNil(client) {
		client = nil
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Bus{client: client, logger: logger, clk: clockwork.NewRealClock()}
}

// resubscribePolicy spaces connection attempts; about 34 hours of failed
// attempts end the subscription.
func resubscribePolicy() retry.Policy {
	return retry.Policy{
		Initial:     100 * time.Millisecond,
		Max:         30 * time.Second,
		Multiplier:  2,
		Jitter:      0.2,
		MaxAttempts: 1 << 12,
	}
}

// Publish encodes msg.Data and PUBLISHes it to the msg.Topic channel. Errors
// are logged — the [pubsub.Bus] contract is fire-and-forget; use
// [Bus.TryPublish] to receive them.
func (b *Bus) Publish(ctx context.Context, msg pubsub.Message) {
	if err := b.TryPublish(ctx, msg); err != nil {
		b.logger.ErrorContext(ctx, "pubsub/redis: publish", slog.String("topic", msg.Topic), slog.Any("err", err))
	}
}

// TryPublish encodes msg.Data and PUBLISHes it to the msg.Topic channel,
// returning a missing client, encode or Redis error.
func (b *Bus) TryPublish(ctx context.Context, msg pubsub.Message) error {
	if b.client == nil {
		return errors.New("pubsub/redis: client is required")
	}
	payload, err := encode(msg.Data)
	if err != nil {
		return err
	}
	if err := b.client.Do(ctx, b.client.B().Publish().Channel(msg.Topic).Message(payload).Build()).Error(); err != nil {
		return fmt.Errorf("pubsub/redis: publish %q: %w", msg.Topic, err)
	}
	return nil
}

// Subscribe SUBSCRIBEs to topic on a dedicated connection in a background
// goroutine and resubscribes after the connection drops. The returned func
// cancels the subscription.
func (b *Bus) Subscribe(topic string, h pubsub.Handler) func() {
	return b.SubscribeWithGap(topic, h, nil)
}

// SubscribeWithGap is [Bus.Subscribe] that calls onGap after every
// resubscribe: Redis drops messages published while the connection was down.
// onGap runs on the subscription goroutine; nil skips it.
func (b *Bus) SubscribeWithGap(topic string, h pubsub.Handler, onGap func()) func() {
	if h == nil {
		return func() {}
	}
	if b.client == nil {
		b.logger.Error("pubsub/redis: client is required", slog.String("topic", topic))
		return func() {}
	}
	stop := make(chan struct{})
	go b.keepSubscribed(stop, topic, h, onGap)
	return sync.OnceFunc(func() { close(stop) })
}

// keepSubscribed holds one subscription session at a time until stop closes,
// the reconnect policy is exhausted, or the session bound is reached.
func (b *Bus) keepSubscribed(stop <-chan struct{}, topic string, h pubsub.Handler, onGap func()) {
	for session := range maxSubscribeSessions {
		lost, release, err := b.resubscribe(stop, topic, h)
		if err != nil {
			if !isClosed(stop) {
				b.logger.Error("pubsub/redis: subscription ended", slog.String("topic", topic), slog.Any("err", err))
			}
			return
		}
		if session > 0 && onGap != nil {
			onGap()
		}
		dropErr := awaitDrop(stop, lost, release)
		if dropErr == nil {
			return
		}
		b.logger.Warn("pubsub/redis: subscription dropped; resubscribing", slog.String("topic", topic), slog.Any("err", dropErr))
	}
	b.logger.Error("pubsub/redis: subscription ended at reconnect bound", slog.String("topic", topic), slog.Int("sessions", maxSubscribeSessions))
}

// resubscribe retries subscribe under resubscribePolicy until it succeeds,
// the policy gives up, or stop closes.
func (b *Bus) resubscribe(stop <-chan struct{}, topic string, h pubsub.Handler) (lost <-chan error, release func(), err error) {
	policy := resubscribePolicy()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(policy.MaxAttempts)*(policy.Max+subscribeTimeout))
	defer cancel()
	// Ends a backoff wait as soon as the caller cancels.
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	err = retry.Do(ctx, func(ctx context.Context) error {
		var subErr error
		lost, release, subErr = b.subscribe(ctx, topic, h)
		return subErr
	}, policy, b.clk)
	if err != nil {
		return nil, nil, fmt.Errorf("pubsub/redis: resubscribe %q: %w", topic, err)
	}
	return lost, release, nil
}

// subscribe SUBSCRIBEs topic on a dedicated connection. lost yields why the
// connection stopped delivering; release returns it to the pool.
func (b *Bus) subscribe(ctx context.Context, topic string, h pubsub.Handler) (lost <-chan error, release func(), err error) {
	ctx, cancel := context.WithTimeout(ctx, subscribeTimeout)
	defer cancel()
	dedicated, release := b.client.Dedicate()
	lost = dedicated.SetPubSubHooks(rueidis.PubSubHooks{OnMessage: func(m rueidis.PubSubMessage) {
		h(pubsub.Message{Topic: m.Channel, Data: []byte(m.Message)})
	}})
	if err = dedicated.Do(ctx, dedicated.B().Subscribe().Channel(topic).Build()).Error(); err != nil {
		release()
		return nil, nil, fmt.Errorf("pubsub/redis: subscribe %q: %w", topic, err)
	}
	return lost, release, nil
}

func isClosed(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// awaitDrop blocks until stop closes (nil) or the connection stops delivering
// (its error), then releases the connection.
func awaitDrop(stop <-chan struct{}, lost <-chan error, release func()) error {
	defer release()
	select {
	case <-stop:
		return nil
	case err, ok := <-lost:
		if !ok || err == nil {
			return errHooksStopped
		}
		return fmt.Errorf("pubsub/redis: connection lost: %w", err)
	}
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
var Module = fx.Module(
	"golusoris.realtime.pubsub.redis",
	fx.Provide(func(c rueidis.Client, logger *slog.Logger) pubsub.Bus {
		return New(c, logger)
	}),
)
