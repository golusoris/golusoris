<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/fcm

Firebase Cloud Messaging (HTTP v1) sender for `notify.Sender`.

## Surface

- `fcm.NewSender(Options)` → `*Sender`.
- `Options{ServiceAccountJSON, ServiceAccount, Scope, Endpoint, HTTPClient,
 MaxResponseBytes}`.
- `fcm.ServiceAccount{Type, ProjectID, PrivateKey, ClientEmail, TokenURI}`.

## Notes

- **Auth**: Google service-account JSON key → RS256-signed JWT
 assertion → OAuth2 token exchange → bearer access token. token
 is cached in memory until < 5 min before expiry; concurrent Send
 calls share single exchange via mutex.
- Dep: `golang-jwt/jwt/v5` (already in go.mod) for JWT signing.
- Injected clients are cloned; missing timeouts become 10s. OAuth token JSON
 defaults to 1 MiB maximum.
- **Routing**: each entry in `msg.To` is one device registration
 token. FCM's v1 API is single-recipient; fan-out uses topics
 (`/topics/<name>`) via direct API call outside this wrapper.
- **Payload**: `msg.Subject` → notification title, `msg.Body` (or
 `msg.Text`) → notification body. `msg.Metadata` becomes FCM `data`
 (string-valued; FCM requires string values everywhere in `data`).
- **Endpoint override**: tests point `Options.Endpoint` at  httptest server; token URI is read from service account's
 `token_uri` field (also overridable in tests).
- **Error surface**: non-2xx responses from send endpoint return
 error that includes status code + first 1 KiB of body. Apps that
 need fine-grained device-state detection (e.g. 404 `UNREGISTERED`
 → delete device row) should inspect returned error or switch to
 direct API use.
- **Topics / multicast**: not wired. Multicast is deprecated in v1
 (use topics instead). For >1000 tokens, iterate or adopt topic
 subscriptions; per-recipient loop here is adequate for  common "ping-me-on-my-phone" pattern.
