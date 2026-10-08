<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — hw/robotics/

Thin wrapper over gobot for orchestrating robotic platforms (Arduino, Raspberry
Pi, drones). Stateless utility — **no fx wiring**. Own go.mod sub-module; import
directly: `github.com/golusoris/golusoris/hw/robotics`.

## API

```go
master := robotics.NewMaster()                 // wraps gobot.Manager
master.AddRobot(robotics.NewRobot("name", workFn))
master.Start()                                 // blocks until Stop
master.Stop()

// re-exports so callers need no direct gobot import:
type Robot = gobot.Robot
type Connection = gobot.Connection
type Device = gobot.Device
```

## Why gobot.io/x/gobot/v2

Single API across many hardware adapters (Firmata, GPIO, drone SDKs); `Manager` lifecycle (add robots → Start/Stop) maps cleanly onto wrapper.

## Notes

- Separate go.mod because gobot pulls CGO adapters for each hardware platform.
- `Master.Start` blocks — run it on its own goroutine if app does other work.
- `NewRobot` wires empty connection/device set; add hardware via  re-exported `gobot.Connection` / `gobot.Device` types directly.
