# notify/twilio

Twilio SMS sender for `notify.Sender`.

## Surface

- `twilio.NewSender(Options)` → `*Sender`.
- `Options{AccountSID, AuthToken, From | MessagingServiceSID,
  StatusCallback, Endpoint, HTTPClient}`.

## Notes

- Raw HTTP — no SDK. POSTs form-encoded body to
  `{endpoint}/Accounts/{sid}/Messages.json` with basic auth
  `{AccountSID}:{AuthToken}`.
- Exactly one of `From` (sender number) or `MessagingServiceSID` (Twilio
  Messaging Service MG…) must be set — the constructor errors if both
  or neither.
- Each recipient in `msg.To` is a separate Twilio request (Twilio's
  Messages endpoint is single-recipient). The sender short-circuits on
  the first non-2xx and returns the offending recipient + status.
- Body resolution: `msg.Body` → `msg.Text` → `msg.Subject`.
- Twilio returns `201 Created` with a Message resource JSON body on
  success.
- Attachments / MMS media not wired — add `MediaUrl` form field if
  needed.
