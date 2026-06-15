# Agent guide — pkg/sockmap/

Opt-in fx module for colocated, zero-TCP-stack IPC via an eBPF SK_MSG /
SOCK_OPS sockmap redirect. When a golusoris server and a peer (e.g. a colocated
SvelteKit node process) share a host/VM/pod, payloads are routed directly
between socket buffers by a BPF program, bypassing the loopback TCP/IP stack.
Transparent to app code. Backs sveltesentio D200 Tier 3.

## Linux only · opt-in

Requires a Linux kernel ≥ 5.10 (CO-RE/BTF), a cgroup v2 unified hierarchy, and
CAP_BPF (or CAP_SYS_ADMIN). On non-Linux — and on Linux with `sockmap.enabled =
false` (the default) — the module wires cleanly but attaches nothing. Never
changes golusoris defaults.

## Key API

| Symbol | Purpose |
|---|---|
| `sockmap.Module` | fx module — provides `*Sockmap` + `*Metrics`, wires load (Start) + pre-shutdown cleanup (Stop) |
| `sockmap.ObjectProvider` | `func() ([]byte, error)` returning the compiled CO-RE BPF object |
| `sockmap.DefaultObjectProvider` | serves the bundled SOCK_OPS + SK_MSG object (Linux) |
| `sockmap.BytesProvider(b)` | provider that serves fixed bytes |
| `(*Sockmap).RegisterConn(c)` | insert an *established* socket FD into the sockhash (kernel rejects listen sockets) |
| `sockmap.ActivationListeners()` | systemd socket-activation FD handoff (LISTEN_PID/LISTEN_FDS/LISTEN_FDNAMES) |
| `sockmap.Metrics` | `golusoris_sockmap_{redirected_bytes_total,active_sockets,redirect_errors_total}` |

## Wiring

```go
fx.New(
    golusoris.Core,
    sockmap.Module,                       // no-op unless sockmap.enabled = true
    fx.Provide(sockmap.DefaultObjectProvider), // full redirect; omit for scaffold-only
    fx.Invoke(func(m *sockmap.Sockmap, conn *net.TCPConn) error {
        return m.RegisterConn(conn)       // optional: app-driven insert
    }),
)
```

Config keys (prefix `sockmap`): `enabled`, `pin_path`
(`/sys/fs/bpf/golusoris/sockhash`), `map_name`, `max_entries`, `cgroup_path`
(empty = auto-detect own cgroup v2), `sockops_prog`, `skmsg_prog`,
`min_kernel_major`, `min_kernel_minor`.

## BPF object

Source: `bpf/sockmap.bpf.c`; the compiled `bpf/sockmap.bpf.o` is **checked in**
(bpf2go-style) so the package builds without a clang toolchain. Regenerate with
`go generate ./pkg/sockmap` (needs clang + llvm-strip + libbpf headers). The
sockhash is keyed by the connection 4-tuple (`struct sock_key`, 16 bytes) so
userspace inserts and the SK_MSG redirect agree. In production the SOCK_OPS
program populates the map from kernel context on established-connection
callbacks; `RegisterConn` is a secondary userspace path.

## Ownership boundary

golusoris owns the pinned sockhash + its own FD lifecycle: it removes inserted
entries **before** the socket closes (pre-shutdown cleanup), so the map never
redirects to a destroyed socket. An external loader (sveltesentio
`@sveltesentio/ipc-sockmap`) reads the same pin as a *client*.

## Don't

- Don't insert a *listening* socket via `RegisterConn` — the kernel returns
  EOPNOTSUPP. Only established TCP sockets are accepted; listen sockets are
  populated by the SOCK_OPS program from kernel context.
- Don't expect it off-Linux, on cgroup v1, or unprivileged — it stubs / fails
  loudly. Gate callers on capability.
- Don't load an untrusted BPF object — it runs in-kernel; treat the
  `ObjectProvider` source as trusted supply chain.
- Don't read sockmap *values* from userspace (kernel `struct sock *` can't be
  copied out — returns ENOSPC). Iterate keys instead.
- Don't set `sockmap.enabled = true` as a default — Tier 3 is strictly opt-in.
