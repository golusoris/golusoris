package pubsub_test

import (
	"context"
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
