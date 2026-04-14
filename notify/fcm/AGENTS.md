# notify/fcm

Firebase Cloud Messaging (HTTP v1) sender for `notify.Sender`.

## Surface

- `fcm.NewSender(Options)` → `*Sender`.
- `Options{ServiceAccountJSON, ServiceAccount, Scope, Endpoint, HTTPClient}`.
- `fcm.ServiceAccount{Type, ProjectID, PrivateKey, ClientEmail, TokenURI}`.

## Notes

- **Auth**: Google service-account JSON key → RS256-signed JWT
  assertion → OAuth2 token exchange → bearer access token. The token
  is cached in memory until < 5 min before expiry; concurrent Send
  calls share a single exchange via a mutex.
- Dep: `golang-jwt/jwt/v5` (already in go.mod) for JWT signing.
- **Routing**: each entry in `msg.To` is one device registration
  token. FCM's v1 API is single-recipient; fan-out uses topics
  (`/topics/<name>`) via direct API call outside this wrapper.
- **Payload**: `msg.Subject` → notification title, `msg.Body` (or
  `msg.Text`) → notification body. `msg.Metadata` becomes FCM `data`
  (string-valued; FCM requires string values everywhere in `data`).
- **Endpoint override**: tests point `Options.Endpoint` at an
  httptest server; the token URI is read from the service account's
  `token_uri` field (also overridable in tests).
- **Error surface**: non-2xx responses from the send endpoint return
  an error that includes status code + first 1 KiB of body. Apps that
  need fine-grained device-state detection (e.g. 404 `UNREGISTERED`
  → delete device row) should inspect the returned error or switch to
  direct API use.
- **Topics / multicast**: not wired. Multicast is deprecated in v1
  (use topics instead). For >1000 tokens, iterate or adopt topic
  subscriptions; the per-recipient loop here is adequate for the
  common "ping-me-on-my-phone" pattern.
