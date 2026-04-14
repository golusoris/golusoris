# notify/resend

Resend transactional email sender for `notify.Sender`.

## Surface

- `resend.NewSender(Options)` → `*Sender`.
- `Options{APIKey, From, ReplyTo, Endpoint, HTTPClient}`.

## Notes

- Raw HTTP — no SDK. POSTs JSON to `https://api.resend.com/emails` with
  `Authorization: Bearer <api_key>`. Set `Endpoint` for the EU region
  (`https://api.eu.resend.com`) or to point at a test server.
- `notify.Message.Metadata` becomes Resend `tags` (array of `{name,value}`).
- Attachments: `Attachment.Data` is base64-encoded into the request via
  the JSON encoder's default `[]byte` handling.
- Resend returns `200` with a `{"id":"..."}` body on success.
