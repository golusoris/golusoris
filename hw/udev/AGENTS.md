<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — hw/udev/

Linux device-event monitoring over libudev (via jochenvg/go-udev, CGO).
Stateless utility — **no fx wiring**. Own go.mod sub-module; import directly:
`github.com/golusoris/golusoris/hw/udev`.

## API

```go
mon, err := udev.NewMonitor(ctx)     // starts a netlink monitor
for ev := range mon.Events() {       // ev: Action, Subsystem, DevNode, Properties
    slog.Info("device", "action", ev.Action, "subsystem", ev.Subsystem)
}
```

`NewMonitor` spawns goroutine that fans kernel events onto buffered channel
(cap 64); it stops and closes channel when `ctx` is cancelled.

## Why jochenvg/go-udev

Idiomatic Go binding over libudev's netlink monitor with context-aware
`DeviceChan` — channel maps directly onto `Events()`.

## Notes

- Linux-only CGO (libudev headers required to build) — hence separate go.mod.
- Public API remains buildable elsewhere; `NewMonitor` returns
 `ErrUnsupported` on non-Linux targets or when CGO is disabled.
- Channel is buffered at 64; slow consumer back-pressures kernel-event
 goroutine. Drain `Events()` promptly.
- libudev errors mid-stream are non-fatal — monitor keeps running.
