# go-webauthn/webauthn — v0.11.2 snapshot

Pinned: **v0.11.2**
Source: https://pkg.go.dev/github.com/go-webauthn/webauthn@v0.11.2

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
    WebAuthnIcon()        string   // deprecated — return ""
}
```

## Credential storage

```go
type Credential struct {
    ID              []byte
    PublicKey       []byte
    AttestationType string
    Authenticator   Authenticator   // includes SignCount
    Flags           CredentialFlags
}
```

## golusoris usage

- `auth/passkeys/` — WebAuthn registration + login handlers + TOTP (MFA).

## Links

- Spec: https://www.w3.org/TR/webauthn-3/
- Changelog: https://github.com/go-webauthn/webauthn/blob/main/CHANGELOG.md
