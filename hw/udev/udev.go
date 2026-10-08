//go:build linux && cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package udev

import (
	"context"
	"errors"
	"fmt"
	"maps"

	udevlib "github.com/jochenvg/go-udev"
)

// NewMonitor creates and starts a udev monitor.  Call Events() to receive events.
// The monitor stops when ctx is cancelled.
func NewMonitor(ctx context.Context) (*Monitor, error) {
	u := udevlib.Udev{}
	mon := u.NewMonitorFromNetlink("udev")
	if mon == nil {
		return nil, errors.New("udev: create monitor")
	}
	devCh, errCh, err := mon.DeviceChan(ctx)
	if err != nil {
		return nil, fmt.Errorf("udev: start monitor: %w", err)
	}
	ch := make(chan Event, 64)
	go forwardEvents(ctx, devCh, errCh, ch)
	return &Monitor{ch: ch}, nil
}

func forwardEvents(
	ctx context.Context,
	devices <-chan *udevlib.Device,
	errs <-chan error,
	events chan<- Event,
) {
	defer close(events)
	for ctx.Err() == nil {
		select {
		case dev, ok := <-devices:
			if !ok || !sendEvent(ctx, events, eventFromDevice(dev)) {
				return
			}
		case err, ok := <-errs:
			if !ok || err == nil {
				return
			}
			// libudev reports transient monitor errors here; keep listening.
		case <-ctx.Done():
			return
		}
	}
}

func eventFromDevice(dev *udevlib.Device) Event {
	props := make(map[string]string)
	maps.Copy(props, dev.Properties())
	return Event{
		Action:     dev.Action(),
		Subsystem:  dev.Subsystem(),
		DevNode:    dev.Devnode(),
		Properties: props,
	}
}
