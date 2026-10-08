<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — net/dnsserver/

fx-wired authoritative DNS server over [miekg/dns]. Provides shared
`*dns.ServeMux`; apps register zones/handlers on it. Listens on **both UDP and
TCP** on same address.

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

de-facto Go DNS library — full RR-type coverage, used by CoreDNS; stdlib
has no DNS server.

## Notes

- `OnStart` blocks until **both** UDP and TCP listeners report ready (via
 `NotifyStartedFunc`) or start ctx is cancelled — app start fails fast if  port is taken.
- Default `:5353` is unprivileged (mDNS port); bind `:53` only with right
 capabilities/privileges.
- Register all handlers before `fx.Invoke` runs listeners; mux itself is
 concurrency-safe for serving.
