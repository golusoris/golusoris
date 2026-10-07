<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

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
 Messaging Service MG…) must be set — constructor errors if both
 or neither.
- Each recipient in `msg.To` is separate Twilio request (Twilio's
 Messages endpoint is single-recipient). sender short-circuits on
 first non-2xx and returns offending recipient + status.
- Body resolution: `msg.Body` → `msg.Text` → `msg.Subject`.
- Twilio returns `201 Created` with Message resource JSON body on
 success.
- Attachments / MMS media not wired — add `MediaUrl` form field if
 needed.
