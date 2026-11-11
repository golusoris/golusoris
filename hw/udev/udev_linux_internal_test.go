//go:build linux && cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package udev

import (
	"context"
	"errors"
	"testing"
	"time"

	udevlib "github.com/jochenvg/go-udev"
)

func TestForwardEventsStops(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		prepare func(context.CancelFunc, chan *udevlib.Device, chan error)
	}{
		{
			name: "context_already_cancelled",
			prepare: func(cancel context.CancelFunc, _ chan *udevlib.Device, _ chan error) {
				cancel()
			},
		},
		{
			name: "devices_closed",
			prepare: func(_ context.CancelFunc, devices chan *udevlib.Device, _ chan error) {
				close(devices)
			},
		},
		{
			name: "errors_closed",
			prepare: func(_ context.CancelFunc, _ chan *udevlib.Device, errs chan error) {
				close(errs)
			},
		},
		{
			name: "nil_error",
			prepare: func(_ context.CancelFunc, _ chan *udevlib.Device, errs chan error) {
				errs <- nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			devices := make(chan *udevlib.Device)
			errs := make(chan error, 1)
			events := make(chan Event)
			tc.prepare(cancel, devices, errs)
			go forwardEvents(ctx, devices, errs, events)
			waitEventsClosed(t, events)
		})
	}
}

func TestForwardEventsContinuesAfterError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	devices := make(chan *udevlib.Device)
	errs := make(chan error)
	events := make(chan Event)
	go forwardEvents(ctx, devices, errs, events)
	select {
	case errs <- errors.New("transient monitor error"):
	case <-events:
		t.Fatal("events closed before the first error")
	case <-time.After(5 * time.Second):
		t.Fatal("forwardEvents did not receive the first error within 5s")
	}
	// A second unbuffered send proves the loop survived the first error.
	select {
	case errs <- errors.New("another transient monitor error"):
	case <-events:
		t.Fatal("events closed after a non-nil error")
	case <-time.After(5 * time.Second):
		t.Fatal("forwardEvents did not continue after a non-nil error within 5s")
	}
	select {
	case <-events:
		t.Fatal("events closed before the devices channel")
	default:
	}
	close(devices)
	waitEventsClosed(t, events)
}

func TestMonitorEvents(t *testing.T) {
	t.Parallel()
	closed := make(chan Event)
	close(closed)
	cases := []struct {
		name string
		ch   <-chan Event
	}{
		{name: "open", ch: make(chan Event)},
		{name: "closed", ch: closed},
		{name: "nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			monitor := Monitor{ch: tc.ch}
			if got := monitor.Events(); got != tc.ch {
				t.Fatalf("Events() = %v, want original channel %v", got, tc.ch)
			}
		})
	}
}

func TestNewMonitorCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	monitor, err := NewMonitor(ctx)
	if err != nil {
		t.Skipf("libudev netlink monitor unavailable: %v", err)
	}
	if monitor == nil {
		t.Fatal("NewMonitor() = nil without an error")
	}
	waitEventsClosed(t, monitor.Events())
}

func waitEventsClosed(t *testing.T, events <-chan Event) {
	t.Helper()
	select {
	case event, ok := <-events:
		if ok {
			t.Fatalf("events delivered %+v, want closed channel", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("events did not close within 5s")
	}
}
