// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build soak

package pubsub_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/realtime/pubsub"
	"github.com/golusoris/golusoris/testutil/load"
)

//nolint:paralleltest // WHY: soak tests sample process-wide metrics; parallel tests skew goroutines and heap.
func TestPubSub_Soak(t *testing.T) {
	bus := pubsub.New()
	var delivered atomic.Int64
	const topic = "soak.events"
	const subscribers = 4

	for range subscribers {
		cancel := bus.Subscribe(topic, func(_ pubsub.Message) {
			delivered.Add(1)
		})
		t.Cleanup(cancel)
	}

	var counter atomic.Int64
	op := func(ctx context.Context) error {
		id := counter.Add(1)
		bus.Publish(ctx, pubsub.Message{Topic: topic, Data: id})
		if id%25 == 0 {
			unsub := bus.Subscribe(topic, func(_ pubsub.Message) {})
			unsub()
		}
		return nil
	}

	res := load.RunSoak(t, load.SoakOptions{
		Name:        "realtime/pubsub",
		Pace:        time.Millisecond,
		Concurrency: 2,
	}, op)

	pub := counter.Load()
	deliv := delivered.Load()
	if pub == 0 || res.TotalOps == 0 {
		t.Fatal("pubsub soak: no messages were published")
	}
	if want := pub * subscribers; deliv != want {
		t.Fatalf("pubsub soak: delivered %d messages, want %d (published %d * %d subscribers)", deliv, want, pub, subscribers)
	}
}
