// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pubsub_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/golusoris/golusoris/realtime/pubsub"
)

func TestPublishSubscribe(t *testing.T) {
	t.Parallel()
	bus := pubsub.New()

	var count atomic.Int32
	cancel := bus.Subscribe("topic.a", func(msg pubsub.Message) {
		count.Add(1)
	})
	defer cancel()

	bus.Publish(context.Background(), pubsub.Message{Topic: "topic.a", Data: "x"})
	bus.Publish(context.Background(), pubsub.Message{Topic: "topic.a", Data: "y"})

	if v := count.Load(); v != 2 {
		t.Errorf("got %d events, want 2", v)
	}
}

func TestSubscribeIgnoresNilHandler(t *testing.T) {
	t.Parallel()
	bus := pubsub.New()
	cancel := bus.Subscribe("topic", nil)
	bus.Publish(t.Context(), pubsub.Message{Topic: "topic"})
	cancel()
}

func TestPublishConcurrentWithSubscriptionChanges(t *testing.T) {
	t.Parallel()
	const iterations = 5_000
	bus := pubsub.New()
	var calls atomic.Int64
	var workers sync.WaitGroup
	workers.Add(2)
	start := make(chan struct{})
	go func() {
		defer workers.Done()
		<-start
		for range iterations {
			cancel := bus.Subscribe("topic", func(pubsub.Message) { calls.Add(1) })
			cancel()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range iterations {
			bus.Publish(context.Background(), pubsub.Message{Topic: "topic"})
		}
	}()
	close(start)
	workers.Wait()
}

func TestUnsubscribe(t *testing.T) {
	t.Parallel()
	bus := pubsub.New()

	var count atomic.Int32
	cancel := bus.Subscribe("t", func(_ pubsub.Message) { count.Add(1) })
	bus.Publish(context.Background(), pubsub.Message{Topic: "t"})
	cancel()
	bus.Publish(context.Background(), pubsub.Message{Topic: "t"})

	if v := count.Load(); v != 1 {
		t.Errorf("got %d, want 1 (unsubscribe should stop delivery)", v)
	}
}

func TestTopicIsolation(t *testing.T) {
	t.Parallel()
	bus := pubsub.New()

	var a, b atomic.Int32
	defer bus.Subscribe("a", func(_ pubsub.Message) { a.Add(1) })()
	defer bus.Subscribe("b", func(_ pubsub.Message) { b.Add(1) })()

	bus.Publish(context.Background(), pubsub.Message{Topic: "a"})

	if a.Load() != 1 || b.Load() != 0 {
		t.Errorf("topic isolation broken: a=%d b=%d", a.Load(), b.Load())
	}
}
