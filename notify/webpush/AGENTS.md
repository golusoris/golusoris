<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/webpush

Browser Web Push sender (RFC 8030 + VAPID RFC 8292) for `notify.Sender`.

## Surface

- `webpush.NewSender(Options)` → `*Sender`.
- `Options{VAPIDPublicKey, VAPIDPrivateKey, Subject, TTL, Urgency,
 Topic, HTTPClient}`.
- `webpush.NewVAPIDKeys()` → generates fresh P-256 keypair
 (URL-safe-base64). Call ONCE, persist private key as secret.
- `webpush.EncodeSubscription(endpoint, p256dh, auth)` → JSON string
 you can stash into `notify.Message.Metadata["subscription"]`.

## Notes

- Depends on [SherClockHolmes/webpush-go](https://github.com/SherClockHolmes/webpush-go)
 — de-facto Go client. Lightweight (one transitive dep:
 `golang.org/x/crypto`).
- Routing: `msg.Metadata["subscription"]` must be JSON-encoded
 browser `PushSubscription` object produced by `PushManager.subscribe()`.
 endpoint is inside that JSON, so this sender doesn't use
 `msg.To`.
- Payload: `msg.Body` is sent verbatim to service worker. Most
 apps put small JSON blob there and decode it in SW's `push`
 event handler.
- TTL defaults to 86400s. `Urgency` accepts `very-low` / `low` /
 `normal` / `high` (per RFC 8030 §5.3). `Topic` collapses duplicate
 notifications with same topic string.
- VAPID `Subject` should be `mailto:` URI — some push services
 (Safari, Firefox) will reject messages without one.
- 410 Gone from push service means subscription is dead —
 apps should delete it from their store. We surface this as  `status 410` error; callers should switch on it.
