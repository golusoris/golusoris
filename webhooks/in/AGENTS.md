# Agent guide — webhooks/in/

Inbound webhook signature verification middleware. No fx dependency — plain
`func(http.Handler) http.Handler` factory functions.

## Providers

| Function | Header verified | Algorithm |
|---|---|---|
| `Stripe(secret)` | `Stripe-Signature` | HMAC-SHA256 + 5-min timestamp replay guard |
| `GitHub(secret)` | `X-Hub-Signature-256` | HMAC-SHA256 |
| `GitHubLegacy(secret)` | `X-Hub-Signature` | HMAC-SHA1 (deprecated, prefer `GitHub`) |
| `Slack(secret)` | `X-Slack-Signature` + `X-Slack-Request-Timestamp` | v0 HMAC-SHA256 + 5-min replay guard |
| `HMAC(secret, header)` | configurable header | HMAC-SHA256, format `sha256=<hex>` |

## Usage

```go
mux.Handle("/webhooks/stripe",  in.Stripe(secret)(stripeHandler))
mux.Handle("/webhooks/github",  in.GitHub(secret)(githubHandler))
mux.Handle("/webhooks/generic", in.HMAC(secret, "X-My-Sig")(myHandler))
```

## Internals

Body is buffered up to `MaxBodyBytes` (1 MiB) for HMAC computation, then
re-placed on `r.Body` so the downstream handler can read it again.

## Don't

- Don't add provider functions without a nolint justification for any SHA-1/MD5 use.
- Don't skip the timestamp check for Stripe/Slack — it prevents replay attacks.
