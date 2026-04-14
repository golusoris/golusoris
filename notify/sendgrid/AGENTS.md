# notify/sendgrid

SendGrid transactional email sender for `notify.Sender`.

## Surface

- `sendgrid.NewSender(Options)` → `*Sender`.
- `Options{APIKey, From, FromName, ReplyTo, Endpoint, HTTPClient}`.

## Notes

- Raw HTTP — no SDK. POSTs JSON to
  `https://api.sendgrid.com/v3/mail/send` with `Authorization: Bearer <api_key>`.
- Uses SendGrid v3 `personalizations` structure: single personalization
  with To/CC/BCC collapsed into it. Rare multi-personalization use
  cases aren't exposed yet.
- `Content` is emitted as `text/plain` first, then `text/html` — v3
  spec-compliant order.
- `notify.Message.Metadata` becomes `custom_args` (passed through to
  webhook events).
- SendGrid returns `202 Accepted` with empty body on success.
