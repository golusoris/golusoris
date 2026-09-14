<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — systemd

sd_notify + watchdog for processes run as systemd units. Zero deps (unixgram to NOTIFY_SOCKET).

## Conventions

- `systemd.Module` is safe to wire unconditionally — no-op when NOTIFY_SOCKET is unset. Apps not running under systemd pay zero cost.
- Matching unit file:

  ```ini
  [Service]
  Type=notify
  NotifyAccess=main
  WatchdogSec=30s
  Restart=on-failure
  ```

- Linux-only by nature: `NOTIFY_SOCKET` is a unixgram socket that only systemd creates. The package compiles everywhere and stays a no-op off-Linux; `TestNotifyWritesToSocket` (the only test that binds a unixgram socket) is `t.Skip`ped on Windows.
- The watchdog ticker fires at WATCHDOG_USEC / 2 (the systemd-recommended rate). If pets fail, systemd kills + restarts per unit policy — that's the desired failure mode.

## Don't

- Don't call `Notify()` from a goroutine hot path — it opens a new unixgram socket each call. For heartbeats use `Module` (one long-lived ticker).
- Don't set `Type=notify` without also wiring `systemd.Module` or sending READY=1 yourself — systemd will kill the unit after `TimeoutStartSec=90s`.
