# Agent guide — ebpf/

fx-wired scaffold for loading and managing eBPF programs via
[cilium/ebpf](https://github.com/cilium/ebpf). The module loads a compiled eBPF
collection on fx Start and runs registered `Loader` callbacks to attach
programs to kernel hooks.

## Linux only

Requires a Linux kernel ≥ 5.7 with `CAP_BPF` (or `CAP_SYS_ADMIN`). On
non-Linux platforms the module is a no-op stub.

## Key API

| Symbol | Purpose |
|---|---|
| `ebpf.Module` | fx module — provides `*Registry`, loads collection on Start |
| `ebpf.ObjectProvider` | `func() ([]byte, error)` returning compiled ELF bytes |
| `ebpf.BytesProvider(b)` | provider that serves fixed bytes (from bpf2go) |
| `ebpf.Loader` | `func(*ebpf.Collection) error` — attaches programs to hooks |
| `Registry.Register(name, Loader)` | register an attach callback |

`Module` requires an `ObjectProvider` and `*slog.Logger` in the graph.

## Workflow

```
1. Write eBPF C source (bpf/xdp_drop.c)
2. clang -O2 -target bpf -c bpf/xdp_drop.c -o bpf/xdp_drop.o
3. go generate ./ebpf/...        # bpf2go → Go bindings + embedded bytes
4. Provide ebpf.BytesProvider(bpfObjects)
5. reg.Register("xdp_drop", func(coll *ebpf.Collection) error { link.AttachXDP(...) })
```

## Don't

- Don't let an attached `link.Link` go out of scope — store it somewhere live,
  or the program detaches when it's GC'd.
- Don't assume it works off-Linux or without privileges — gate callers behind a
  capability check; the module stubs out elsewhere.
- Don't load untrusted `.o` bytes — eBPF programs run in-kernel; treat the
  `ObjectProvider` source as part of your trusted supply chain.
- Don't do heavy work inside a `Loader` — it runs during fx Start and blocks
  startup; just attach and return.
