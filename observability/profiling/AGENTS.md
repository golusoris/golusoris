<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/profiling

Continuous in-process profiling via grafana/pyroscope-go.

## Conventions

- Off by default. Set `profiling.enabled=true` + `profiling.app=<name>` to start.
- Profiles collected: CPU, alloc_objects, alloc_space, inuse_objects, inuse_space, goroutines. Apps that need fewer can fork Start helper.
- Lifecycle-managed: started on fx OnStart, stopped on OnStop.

## eBPF mode

Node-wide eBPF profiling is deployment responsibility. Repo ships no eBPF
profiler manifests. If app adds one, both modes may write to same Pyroscope
server: in-process agent covers app code; eBPF agent covers host + syscall layer.

## Don't

- Don't enable in-process profiling on very small pods (<200m CPU). agent overhead becomes noticeable.
