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

func TestPubSub_Soak(t *testing.T) {
	t.Parallel()
	bus := pubsub.New()
	var delivered atomic.Int64
	const topic = "soak.events"

	for range 4 {
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

	load.RunSoak(t, load.SoakOptions{
		Name:        "realtime/pubsub",
		Pace:        time.Millisecond,
		Concurrency: 2,
	}, op)
}
