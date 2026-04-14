// Package udev provides Linux device event monitoring using libudev via
// jochenvg/go-udev (CGO).
//
// This is a separate go.mod sub-module because libudev is Linux-only CGO.
// Import directly: github.com/golusoris/golusoris/hw/udev
//
// # Usage
//
//	mon, err := udev.NewMonitor(ctx)
//	for ev := range mon.Events() {
//	    slog.Info("device event", "action", ev.Action, "subsystem", ev.Subsystem, "devnode", ev.DevNode)
//	}
package udev

import (
	"context"
	"fmt"

	udevlib "github.com/jochenvg/go-udev"
)

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

// NewMonitor creates and starts a udev monitor.  Call Events() to receive events.
// The monitor stops when ctx is cancelled.
func NewMonitor(ctx context.Context) (*Monitor, error) {
	u := udevlib.Udev{}
	mon := u.NewMonitorFromNetlink("udev")
	if mon == nil {
		return nil, fmt.Errorf("udev: create monitor")
	}
	devCh, errCh, err := mon.DeviceChan(ctx)
	if err != nil {
		return nil, fmt.Errorf("udev: start monitor: %w", err)
	}
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		for {
			select {
			case dev, ok := <-devCh:
				if !ok {
					return
				}
				props := make(map[string]string)
				for k, v := range dev.Properties() {
					props[k] = v
				}
				ch <- Event{
					Action:     dev.Action(),
					Subsystem:  dev.Subsystem(),
					DevNode:    dev.Devnode(),
					Properties: props,
				}
			case err, ok := <-errCh:
				if !ok || err == nil {
					return
				}
				// non-fatal; keep running
			case <-ctx.Done():
				return
			}
		}
	}()
	return &Monitor{ch: ch}, nil
}

// Events returns the channel of device events.
func (m *Monitor) Events() <-chan Event { return m.ch }
