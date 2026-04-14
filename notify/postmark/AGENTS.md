# notify/postmark

Postmark transactional email sender for `notify.Sender`.

## Surface

- `postmark.NewSender(Options)` → `*Sender`.
- `Options{ServerToken, From, ReplyTo, MessageStream, Endpoint, HTTPClient}`.

## Notes

- Raw HTTP — no SDK. POSTs JSON to `https://api.postmarkapp.com/email`
  with `X-Postmark-Server-Token: <token>`. Set `Endpoint` to point at a
  test server.
- `MessageStream` selects the stream (`outbound` for transactional;
  `broadcast` for marketing). Defaults unset → Postmark uses the
  server's default stream.
- Postmark expects comma-separated `To`/`Cc`/`Bcc` strings — the sender
  joins `notify.Message.To` accordingly.
- `notify.Message.Metadata` is forwarded as the Postmark `Metadata`
  object verbatim.
- Attachments: `Attachment.Data` is base64-encoded into the request via
  the JSON encoder's default `[]byte` handling.
