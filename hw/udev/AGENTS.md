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

`NewMonitor` spawns a goroutine that fans kernel events onto a buffered channel
(cap 64); it stops and closes the channel when `ctx` is cancelled.

## Why jochenvg/go-udev

Idiomatic Go binding over libudev's netlink monitor with a context-aware
`DeviceChan` — the channel maps directly onto `Events()`.

## Notes

- Linux-only CGO (libudev headers required to build) — hence the separate go.mod.
- Channel is buffered at 64; a slow consumer back-pressures the kernel-event
  goroutine. Drain `Events()` promptly.
- libudev errors mid-stream are non-fatal — the monitor keeps running.
