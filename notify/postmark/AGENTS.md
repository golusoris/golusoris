<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/postmark

Postmark transactional email sender for `notify.Sender`.

## Surface

- `postmark.NewSender(Options)` → `*Sender`.
- `Options{ServerToken, From, ReplyTo, MessageStream, Endpoint, HTTPClient}`.
- `postmark.NewBasicAuthVerifier(username, password)` → `(WebhookVerifier, error)`.
- `WebhookVerifier` authenticates bounded raw webhook body before parse/dispatch.

## Notes

- Raw HTTP — no SDK. POSTs JSON to `https://api.postmarkapp.com/email`
 with `X-Postmark-Server-Token: <token>`. Set `Endpoint` to point at  test server.
- `MessageStream` selects stream (`outbound` for transactional;
 `broadcast` for marketing). Defaults unset → Postmark uses  server's default stream.
- Postmark expects comma-separated `To`/`Cc`/`Bcc` strings — sender
 joins `notify.Message.To` accordingly.
- `notify.Message.Metadata` is forwarded as Postmark `Metadata`
 object verbatim.
- Attachments: `Attachment.Data` is base64-encoded into request via
 JSON encoder's default `[]byte` handling.
- Postmark webhooks carry no payload signature. Configure HTTP Basic Auth with
 `NewBasicAuthVerifier`; add Postmark source-IP allowlist at deployment edge.
- Empty Basic Auth credentials reject during construction. Credential checks use
 fixed-size SHA-256 digests plus constant-time comparison.
