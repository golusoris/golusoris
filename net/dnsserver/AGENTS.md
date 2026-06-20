# Agent guide — net/dnsserver/

fx-wired authoritative DNS server over [miekg/dns]. Provides a shared
`*dns.ServeMux`; apps register zones/handlers on it. Listens on **both UDP and
TCP** on the same address.

## Wiring

```go
fx.New(
    dnsserver.Module, // provides *dns.ServeMux, starts UDP+TCP listeners
    fx.Invoke(func(mux *dns.ServeMux) {
        mux.HandleFunc("example.com.", handler) // dns.HandlerFunc
    }),
)
```

- **Provides:** `*dns.ServeMux`.
- **Requires:** `*config.Config`, `*slog.Logger`.
- **Config prefix:** `dns` (env `APP_DNS_*`).

```
dns.addr      # listen address (default: :5353)
dns.udp_size  # max UDP message size in bytes (default: 4096)
```

## Why miekg/dns

The de-facto Go DNS library — full RR-type coverage, used by CoreDNS; stdlib
has no DNS server.

## Notes

- `OnStart` blocks until **both** the UDP and TCP listeners report ready (via
  `NotifyStartedFunc`) or the start ctx is cancelled — app start fails fast if a
  port is taken.
- Default `:5353` is unprivileged (mDNS port); bind `:53` only with the right
  capabilities/privileges.
- Register all handlers before `fx.Invoke` runs the listeners; the mux itself is
  concurrency-safe for serving.
