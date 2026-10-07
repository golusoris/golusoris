// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package udev provides Linux device event monitoring using libudev via
// jochenvg/go-udev. The API builds on every Go platform; [NewMonitor] returns
// [ErrUnsupported] unless the target is Linux with CGO enabled.
package udev

import (
	"context"
	"errors"
)

// ErrUnsupported reports that libudev is unavailable on the target platform.
var ErrUnsupported = errors.New("udev: requires Linux with CGO")

// Event holds a udev device event.
type Event struct {
	// Action is "add", "remove", "change", etc.
	Action string
	// Subsystem is the kernel subsystem (e.g. "usb", "block", "net").
	Subsystem string
	// DevNode is the /dev path (may be empty for non-block/char devices).
	DevNode string
	// Properties contains raw udev properties.
	Properties map[string]string
}

// Monitor subscribes to kernel udev events.
type Monitor struct {
	ch <-chan Event
}

func sendEvent(ctx context.Context, events chan<- Event, event Event) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// Events returns the channel of device events.
func (m *Monitor) Events() <-chan Event { return m.ch }
