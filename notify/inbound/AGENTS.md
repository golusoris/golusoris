# notify/inbound

Inbound email handlers for AWS SES (via SNS), Postmark, and raw
RFC 5322 MIME blobs from SMTP servers. Normalizes to a single `Email`.

## Surface

- `inbound.SES(HandlerFunc) http.Handler` — SES/SNS inbound webhook.
  Parses the MIME `content` inline when the SES rule action is SNS;
  fires a bare event (just headers) when the action is S3 so apps can
  fetch the object themselves.
- `inbound.Postmark(HandlerFunc) http.Handler` — Postmark inbound
  webhook (JSON with pre-parsed `TextBody` / `HtmlBody`).
- `inbound.ParseMIME([]byte) (Email, error)` — parse raw RFC 5322
  (useful for SMTP handoff from `net/smtpserver`).
- `Email{MessageID, From, To, CC, Subject, Text, HTML, RawHeaders, ReceivedAt, Provider}`.

## Notes

- Signature verification is out of scope — mount behind `webhooks/in`
  or an SNS-aware proxy when exposed publicly.
- Body size capped at 25 MiB (typical provider cap).
- `Subject` is decoded via `mime.WordDecoder` (RFC 2047) when parsing
  raw MIME.
- `ParseMIME` emits the raw body into `Text` without MIME-part walking;
  multipart decomposition is left to callers who need it (use
  `github.com/emersion/go-message` for full MIME tree traversal).
