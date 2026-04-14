# notify/bounce

Bounce + complaint webhook handlers for AWS SES (via SNS) and Postmark.
Normalizes both into a single `Event` and forwards to a `HandlerFunc`.

## Surface

- `bounce.SES(HandlerFunc) http.Handler`  — accepts SNS-wrapped SES
  bounce/complaint/delivery notifications. Acknowledges
  `SubscriptionConfirmation` messages with 200 (the app decides whether
  to fetch `SubscribeURL`).
- `bounce.Postmark(HandlerFunc) http.Handler` — accepts Postmark
  bounce + spam-complaint payloads.
- `Event{Kind, Email, MessageID, Subtype, Permanent, Reason, Timestamp, Provider}`.
- `ev.Permanent()` — true when the bounce should trigger suppression
  (`Permanent` SES bounces, all complaints, Postmark `HardBounce` /
  `SpamComplaint` / `CanActivate=false`).

## Notes

- Signature verification is **out of scope** — mount behind
  `webhooks/in` or an SNS-aware proxy when exposed publicly.
- Body size is capped at 1 MiB.
- Integrates with `notify/unsub`: common pattern is to forward
  `Permanent` events to `unsub.Store.Add(ctx, ev.Email)`.
