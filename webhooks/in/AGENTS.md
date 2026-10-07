<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — webhooks/in/

Inbound webhook signature verification middleware. No fx dependency. Each
factory returns `(func(http.Handler) http.Handler, error)` and rejects empty
signing secret before routes are mounted.

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
stripe, err := in.Stripe(secret)
if err != nil { return err }
github, err := in.GitHub(secret)
if err != nil { return err }
generic, err := in.HMAC(secret, "X-My-Sig")
if err != nil { return err }
mux.Handle("/webhooks/stripe", stripe(stripeHandler))
mux.Handle("/webhooks/github", github(githubHandler))
mux.Handle("/webhooks/generic", generic(myHandler))
```

## Internals

Body is buffered up to `MaxBodyBytes` (1 MiB) for HMAC computation, then
re-placed on `r.Body` so downstream handler can read it again. Larger bodies
are rejected with 413; signed prefixes never pass.

## Don't

- Don't add provider functions without nolint justification for any SHA-1/MD5 use.
- Keep symmetric timestamp window for Stripe/Slack — past and future
  timestamps outside five minutes are rejected to prevent replay attacks.
