# Agent guide — observability/profiling

Continuous in-process profiling via grafana/pyroscope-go.

## Conventions

- Off by default. Set `profiling.enabled=true` + `profiling.app=<name>` to start.
- Profiles collected: CPU, alloc_objects, alloc_space, inuse_objects, inuse_space, goroutines. Apps that need fewer can fork the Start helper.
- Lifecycle-managed: started on fx OnStart, stopped on OnStop.

## eBPF mode

Node-wide profiling via eBPF is NOT in this Go package — it runs as a daemonset. See `deploy/observability/pyroscope-ebpf/` (Step 21) for the manifests. Both modes write to the same Pyroscope server; the in-process agent covers app-level code, the eBPF agent covers the host + syscall layer.

## Don't

- Don't enable in-process profiling on very small pods (<200m CPU). The agent overhead becomes noticeable.
