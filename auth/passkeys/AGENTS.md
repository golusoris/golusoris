# auth/passkeys

WebAuthn (passkeys) + TOTP MFA wrappers around go-webauthn/webauthn and pquerna/otp.

## Surface

- `passkeys.New(opts)` — relying-party config (`RPID`, `RPName`, `RPOrigins`).
- Registration: `BeginRegistration(user)` → send to browser; `FinishRegistration(user, sess, r)` → persist credential.
- Login: `BeginLogin(user)` / `FinishLogin(user, sess, r)`.
- TOTP: `ProvisionTOTP(issuer, account)` → `*otp.Key` (show URL as QR); `VerifyTOTP(secret, code)`.

## Notes

- `User` is the `webauthn.User` interface — apps adapt their model.
- Persisting `Credential`s and round-tripping `SessionData` is the app's responsibility.
- TOTP uses SHA-1 / 6 digits / 30s period (the standard Google Authenticator profile) and accepts ±1 period clock skew.
