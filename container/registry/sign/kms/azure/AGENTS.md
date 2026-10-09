<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/sign/kms/azure/

`crypto.Signer` over Azure Key Vault / Managed HSM key: key never leaves vault, signs for `sign.Image`. Own go.mod; non-test code = `azkeys` + `azidentity` + `azcore`.

## API

```go
s, err := azure.New(ctx, azure.Config{
    Vault: "https://signing.vault.azure.net", // https://host[:port]; Managed HSM too
    Key:   "image-signing",
    // Version: "<32 hex>",     // empty: current version pinned at New
    // Credential: cred,        // nil: azidentity.NewDefaultAzureCredential
    // Transport: rt,           // vault + default-credential token requests
})
sig, err := sign.Image(ctx, client, ref, sign.Signer{Key: s}, sign.Options{})
sigs, err := sign.Verify(ctx, client, ref, sign.Policy{Key: s.Public(), InsecureIgnoreTlog: true})
```

- Credentials: `Credential` nil -> `NewDefaultAzureCredential` (env, AKS workload identity `AZURE_CLIENT_ID`/`AZURE_TENANT_ID`/`AZURE_FEDERATED_TOKEN_FILE`, managed identity, Azure CLI; `AZURE_TOKEN_CREDENTIALS` narrows). Token scope from Key Vault bearer challenge; challenge resource verification on (`DisableChallengeResourceVerification` not exposed).
- `New`: validates config before I/O (`ErrInvalidConfig`): vault https only (azcore refuses bearer over http), no userinfo/query/fragment/path; key `[0-9A-Za-z-]{1,127}`; version `[0-9A-Za-z]{1,64}`. `GetKey` (version or current); answer `kid` = same vault host, key, version (when set); pins version from `kid`. Rejects disabled, no `sign` op, `oct`/`oct-HSM`, P-256K, off-curve/short coordinates, bad RSA exponent (`ErrUnsupportedKey`).
- Keys: EC/EC-HSM P-256/P-384/P-521 (JWK x/y full length), RSA/RSA-HSM. No Ed25519 in Key Vault.
- `Sign(rand, digest, opts)`: rand unused. EC: curve-bound hash only (P-256 `ES256`/SHA-256, P-384 `ES384`, P-521 `ES512`), no PSS. RSA: `RS256/384/512`, `*rsa.PSSOptions` -> `PS256/384/512`, salt `Auto`, `EqualsHash` or hash size. Digest length checked. Rejections `ErrUnsupportedHash` before I/O.
- Answer: `kid` = pinned version; EC result JOSE r||s (exactly 2x curve size) -> ASN.1 DER; signature verified locally against pinned public key (PSS with caller's salt length).
- Timeout (HISS-02): `Config.Timeout` (default 30s) bounds `GetKey` and each `Sign` whole: challenge round trip, token request, azcore retries (default 3, 429/5xx) under one ctx. `Transport` set -> `http.Client{Timeout}` too.
- koanf tags on scalar fields; no fx module: stateless constructor (Hard Rule 3).

## RBAC

`Key Vault Crypto User` (keys/read + keys/sign/action) on key; Managed HSM: `Managed HSM Crypto User`.

## Tests

- `azure_test.go` + `fake_test.go`: httptest TLS Key Vault data-plane fake (`GET keys/<n>[/<v>]`, `POST keys/<n>/<v>/sign`) behind 401 bearer challenge; self-signed cert for `signing.vault.example.com`, transport dials fake for any host; real keys, JOSE r||s answers.
  - Config: invalid table rejected before I/O (0 requests, 0 token requests); name boundaries (127-char key, 32-hex version).
  - Sign: every curve/hash/padding (alg, base64url value, version path asserted, signature verified); version pinning (current, explicit, rotation after `New`, missing version); bad opts before I/O; one 503 retried by azcore; 8 concurrent signs (race).
  - Answers: 403, non-JSON, other version/key/vault `kid`, no `kid`, short or DER signature, foreign signature; key answers: disabled, no sign op, symmetric, no kty, P-256K, no curve, short x, off-curve, curve mismatch, RSA modulus/exponent; foreign `kid` on `GetKey`, no key.
  - Credential: scope from challenge; credential error; foreign challenge resource refused before any token request.
  - Bounds: Timeout bounds `Sign` + `New`; 1ns valid but times out.
- `default_test.go` (not parallel, `t.Setenv`): `NewDefaultAzureCredential` with AKS workload identity variables + `AZURE_AUTHORITY_HOST` -> fake Entra (MSAL instance + tenant discovery, client assertion grant); token cached across 2 signs; rejected assertion.
- `image_test.go`: `sign.Image` -> `sign.Verify` (ggcr in-process registry) for P-256/384/521, RSA via fake; foreign key -> `ErrNoValidSignature`; denied vault -> `sign.Image` fails.
- Gaps: no Key Vault emulator; fake follows data-plane REST shape azkeys v1.5.0 sends. Managed identity (IMDS) path not exercised.

## Notes

- Dependency choice (Hard Rule 2), measured `go list -m all` / go.sum, probe module per option: `azkeys` v1.5.0 + `azidentity` v1.14.1 31/41; sigstore `pkg/signature/kms/azure` v1.11.0 92/69. Both resolve `golang.org/x/net` v0.58 + `x/crypto` v0.55 (8 govulncheck findings); test graph raises x/net to v0.60, x/crypto to v0.57 (repo versions), govulncheck clean.
- Module graph weight = tests only: `sign` (e2e). No root golusoris require (vault notes apply).
