<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — net/wol/

Sends Wake-on-LAN magic packets over UDP broadcast. Stateless utility —
**no fx wiring** (like `hash/`, `markdown/`). Apps import it directly.

## API

```go
err := wol.WakeContext(ctx, "aa:bb:cc:dd:ee:ff")
err  = wol.WakeToContext(ctx, "aa:bb:cc:dd:ee:ff", "192.168.1.255:9")
pkt, err := wol.MagicPacket("aa:bb:cc:dd:ee:ff")
```

`Wake` and `WakeTo` remain compatibility wrappers. Both apply
`DefaultTimeout` (5 seconds). Context variants preserve earlier caller
deadline or cancellation.

MAC accepts colon-, hyphen-separated, or bare 12-hex-char forms. magic packet
is 6×`0xFF` + 16× target MAC (102 bytes). `DefaultBroadcast` is
`255.255.255.255:9`.

## Notes

- No third-party deps or CGO; `core/errors` handles connection close errors.
- Dialer timeout and UDP write deadline both use effective bounded context.
- Cancellation expires active write deadline; no unbounded network I/O.
- limited broadcast `255.255.255.255` is often dropped by routers; for  remote host use target subnet's directed broadcast via `WakeTo`.
- Most NICs listen on UDP port 9 (discard) or 7 (echo); WoL is fire-and-forget,
 there is no delivery confirmation.
