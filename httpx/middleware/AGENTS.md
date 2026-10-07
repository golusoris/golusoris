<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# httpx/middleware

HTTP middleware. Plain `func(http.Handler) http.Handler`.

## Order

canonical stack, outermost first:

```go
r.Use(
    middleware.RequestID,
    middleware.TrustProxy(middleware.TrustProxyOptions{TrustedCIDRs: cidrs}),
    middleware.Recover(logger),
    middleware.Logger(logger, clk),
    middleware.OTel("app", tracerProvider),
    middleware.SecureHeaders(middleware.SecureHeadersDefaults()),
    compress,
    middleware.ETag,
)
```

## Trust

- `RequestID`: always replace inbound ID.
- `RequestIDFromTrustedPeers`: keep valid bounded ID only from direct trusted
  CIDR. Run before `TrustProxy`.
- `TrustProxy`: direct peer must be trusted. Walk XFF right-to-left. First
  untrusted hop wins. Duplicate fields are one chain. Malformed or over-64-hop
  chain is ignored, and forwarding headers from untrusted peers are removed.
- Trusted `X-Forwarded-Proto`: exactly one `http` or `https` value. Read only
  through `ForwardedProtoFromContext`; invalid, chained, or duplicate values
  produce no context value.

## Response

- `Recover`: RFC 9457 before commit. Panic after commit becomes
  `http.ErrAbortHandler`; never append second body.
- `Logger`: status/bytes/elapsed/request ID. Preserves flush, hijack, push,
  ReaderFrom, and unwrap behavior.
- `ETag`: weak hash for bounded GET response. Default buffer cap 1 MiB.
  Oversize, flush, or hijack switches to passthrough. First-call hijack sends
  no synthetic HTTP status before raw connection handoff. Handler-supplied
  validator wins and remains eligible for conditional 304.
- `ETagWithLimit`: explicit cap; nonpositive means pass through.
- OTel nil provider: global provider.

## Don't

- No raw XFF trust outside `TrustProxy`.
- No duplicate access log in handler.
- No ETag middleware on known stream route.
