// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package udev

import (
	"context"
	"testing"
)

func TestSendEventStopsWhenContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if sendEvent(ctx, make(chan Event), Event{}) {
		t.Fatal("sendEvent() = true after cancellation, want false")
	}
}

func TestSendEventDeliversBufferedEvent(t *testing.T) {
	t.Parallel()
	events := make(chan Event, 1)
	want := Event{Action: "add", Subsystem: "usb"}

	if !sendEvent(context.Background(), events, want) {
		t.Fatal("sendEvent() = false, want true")
	}
	if got := <-events; got.Action != want.Action || got.Subsystem != want.Subsystem {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}
