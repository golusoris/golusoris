<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/bounce

Bounce + complaint webhook handlers for AWS SES (via SNS) and Postmark.
Normalizes both into single `Event` and forwards to `HandlerFunc`.

## Surface

- `bounce.SES(SNSVerifier, HandlerFunc) http.Handler` — accepts authenticated SNS-wrapped SES
 bounce/complaint/delivery notifications. Acknowledges
 `SubscriptionConfirmation` messages with 200 (app decides whether
 to fetch `SubscribeURL`).
- `bounce.Postmark(postmark.WebhookVerifier, HandlerFunc) http.Handler` — accepts
 authenticated Postmark bounce + spam-complaint payloads.
- `Event{Kind, Email, MessageID, Subtype, Permanent, Reason, Timestamp, Provider}`.
- `ev.Permanent()` — true when bounce should trigger suppression
 (`Permanent` SES bounces, all complaints, Postmark `HardBounce` /
 `SpamComplaint` / `CanActivate=false`).

## Notes

- `SNSVerifier` mandatory; runs before envelope acknowledgement or dispatch.
 Validate SNS signature, trusted certificate URL/chain, expected `TopicArn`, or
 prove equivalent authentication at trusted proxy.
- Postmark verifier mandatory; nil or failure returns 401 before parse/dispatch.
 Use `postmark.NewBasicAuthVerifier`. Postmark emits no payload signature; add
 source-IP allowlist at deployment edge.
- Body size capped at 1 MiB; bound applies before verification.
- Integrates with `notify/unsub`: common pattern is to forward
 `Permanent` events to `unsub.Store.Add(ctx, ev.Email)`.
