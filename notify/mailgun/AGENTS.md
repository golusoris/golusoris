# notify/mailgun

Mailgun transactional email sender for `notify.Sender`.

## Surface

- `mailgun.NewSender(Options)` → `*Sender`.
- `Options{Domain, APIKey, From, ReplyTo, Endpoint, HTTPClient}`.
- `DefaultEndpoint` (US) / `EURegionEndpoint` (EU) constants.

## Notes

- Raw HTTP — no SDK. POSTs form-encoded body to
  `{endpoint}/{domain}/messages` with basic auth `api:<api_key>`.
- `notify.Message.Metadata` becomes `v:<key>` user-variables, queryable
  in Mailgun's event logs.
- EU region: pass `Endpoint: mailgun.EURegionEndpoint`.
- Attachments are NOT yet wired — Mailgun expects multipart/form-data
  for file attachments; swap to that encoding when we need it.
- Mailgun returns `200 OK` with `{"id":"<...>","message":"Queued"}` on
  success. Any non-2xx is treated as an error and the first 1 KiB of
  response body is included in the error.
