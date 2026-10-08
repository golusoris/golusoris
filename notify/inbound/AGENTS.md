<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/inbound

Inbound email handlers for AWS SES (via SNS), Postmark, and raw
RFC 5322 MIME blobs from SMTP servers. Normalizes to single `Email`.

## Surface

- `inbound.SES(SNSVerifier, HandlerFunc) http.Handler` — authenticated SES/SNS inbound webhook.
 Parses MIME `content` inline when SES rule action is SNS;
 fires bare event (headers) when action is S3 so apps can
 fetch object themselves.
- `inbound.Postmark(postmark.WebhookVerifier, HandlerFunc) http.Handler` — authenticated
 Postmark inbound webhook; JSON provides pre-parsed `TextBody` / `HtmlBody`.
- `inbound.ParseMIME([]byte) (Email, error)` — parse raw RFC 5322
 (useful for SMTP handoff from `net/smtpserver`).
- `Email{MessageID, From, To, CC, Subject, Text, HTML, RawHeaders, ReceivedAt, Provider}`.

## Notes

- `SNSVerifier` mandatory; runs on complete raw envelope before JSON parsing.
 Validate SNS signature, trusted certificate URL/chain, expected `TopicArn`, or
 prove equivalent authentication at trusted proxy.
- Postmark verifier mandatory; nil or failure returns 401 before parse/dispatch.
 Use `postmark.NewBasicAuthVerifier`. Postmark emits no payload signature; add
 source-IP allowlist at deployment edge.
- SNS-action MIME content may be UTF-8 or Base64; both encodings are parsed.
- Body size capped at 25 MiB; bound applies before verification.
- `Subject` is decoded via `mime.WordDecoder` (RFC 2047) when parsing
 raw MIME.
- `ParseMIME` emits raw body into `Text` without MIME-part walking;
 multipart decomposition is left to callers who need it (use
 `github.com/emersion/go-message` for full MIME tree traversal).
