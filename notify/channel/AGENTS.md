<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/channel

Generic channel dispatch + secret redaction.

## Surface

- `channel.Sender(kind, cfg)` -> `notify.Sender`
- `channel.Deliver(ctx, kind, cfg, msg, hook)` -> error; chat kinds ignore hook
- `channel.DeliverWebhook(ctx, cfg, req)` -> error
- `channel.Redact(err, cfg)` -> string

## Kinds

- `discord`: incoming webhook
- `slack`: incoming webhook
- `telegram`: bot API
- `ntfy`: push topic
- `gotify`: push server
- `teams`: MessageCard webhook
- `webhook`: HMAC outbound webhook via `webhooks/out`

## Rules

- Zero recursion.
- Bounded retry: webhook max attempts 3, timeout 10s default.
- Scrub secrets from errors via `Redact`.
