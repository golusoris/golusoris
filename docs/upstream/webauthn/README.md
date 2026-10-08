<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# go-webauthn/webauthn — v0.18.2 snapshot

Pinned: **v0.18.2**
Source: [tagged source](https://github.com/go-webauthn/webauthn/tree/v0.18.2)

## Initialization

```go
import "github.com/go-webauthn/webauthn/webauthn"

wauthn, err := webauthn.New(&webauthn.Config{
    RPDisplayName: "My App",
    RPID:          "example.com",
    RPOrigins:     []string{"https://example.com"},
})
```

## Registration flow

```go
// 1. Begin registration — returns options to send to browser
options, sessionData, err := wauthn.BeginRegistration(user)

// 2. Store sessionData in session store

// 3. Finish registration — parse browser response
credential, err := wauthn.FinishRegistration(user, *sessionData, r)
// Store credential in DB
```

## Authentication flow

```go
// 1. Begin authentication
options, sessionData, err := wauthn.BeginLogin(user)

// 2. Finish authentication
credential, err := wauthn.FinishLogin(user, *sessionData, r)
// Update credential.Authenticator.SignCount in DB
```

## User interface

```go
type User interface {
    WebAuthnID()          []byte
    WebAuthnName()        string
    WebAuthnDisplayName() string
    WebAuthnCredentials() []webauthn.Credential
}
```

## Credential storage

```go
type Credential struct {
    ID                []byte
    PublicKey         []byte
    AttestationType   string
    AttestationFormat string
    Transport         []protocol.AuthenticatorTransport
    Flags             CredentialFlags
    Authenticator     Authenticator          // includes SignCount
    Attestation       CredentialAttestation
    Extensions        CredentialExtensions   // durable registration results
}
```

Persist the complete credential. Write back `Authenticator.SignCount`, clone
state, backup state, and other mutable credential fields after every successful
login. Partition credentials by relying-party ID in application storage.

## v0.18 migration notes

- Registration and login option functions can now return errors; ceremony
  builders stop at the first invalid option.
- Extension inputs and outputs are typed. Build them with
  `WithExtensions` / `WithAssertionExtensions` and the matching
  `WithExtension*` option.
- `Credential.Extensions` stores durable extension results added in v0.18.
- Serialized in-flight `SessionData.Extensions` changed shape. Drain or
  invalidate ceremonies started on an older version before deploying v0.18.
- Attestation and signature policy choices are explicit in verification APIs.

Read the pinned module's `MIGRATION.md` before changing extension,
attestation, signature-policy, metadata, or stored-credential code.

## golusoris usage

- `auth/passkeys/` — WebAuthn registration + login handlers + TOTP (MFA).

## Links

- [WebAuthn Level 3 specification](https://www.w3.org/TR/webauthn-3/)
- [v0.18.2 migration guide](https://github.com/go-webauthn/webauthn/blob/v0.18.2/MIGRATION.md)
- [v0.18.2 changelog](https://github.com/go-webauthn/webauthn/blob/v0.18.2/CHANGELOG.md)
