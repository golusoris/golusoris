# Agent guide — net/wol/

Sends Wake-on-LAN magic packets over UDP broadcast. Stateless utility —
**no fx wiring** (like `hash/`, `markdown/`). Apps import it directly.

## API

```go
err := wol.Wake("aa:bb:cc:dd:ee:ff")                       // → 255.255.255.255:9
err  = wol.WakeTo("aa:bb:cc:dd:ee:ff", "192.168.1.255:9")  // explicit subnet broadcast
pkt, err := wol.MagicPacket("aa:bb:cc:dd:ee:ff")           // build 102-byte frame, don't send
```

MAC accepts colon-, hyphen-separated, or bare 12-hex-char forms. A magic packet
is 6×`0xFF` + 16× the target MAC (102 bytes). `DefaultBroadcast` is
`255.255.255.255:9`.

## Notes

- Pure stdlib (`net`, `encoding/hex`) — no third-party deps, no CGO.
- The limited broadcast `255.255.255.255` is often dropped by routers; for a
  remote host use the target subnet's directed broadcast via `WakeTo`.
- Most NICs listen on UDP port 9 (discard) or 7 (echo); WoL is fire-and-forget,
  there is no delivery confirmation.
