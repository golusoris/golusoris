<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/sign/kms/gcp/

`crypto.Signer` over Google Cloud KMS asymmetric key version: key never leaves Cloud KMS, signs for `sign.Image`. Own go.mod; non-test code = stdlib `net/http` + `golang.org/x/oauth2` (no Cloud KMS SDK).

## API

```go
s, err := gcp.New(ctx, gcp.Config{
    Key: "projects/p/locations/europe-west3/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1",
    // Endpoint: "https://cloudkms.googleapis.com", // default DefaultEndpoint
    // TokenSource: ts,  // nil: Application Default Credentials, Scope cloudkms
    // Transport: rt,    // KMS + token requests; nil = http.DefaultTransport clone
})
sig, err := sign.Image(ctx, client, ref, sign.Signer{Key: s}, sign.Options{})
sigs, err := sign.Verify(ctx, client, ref, sign.Policy{Key: s.Public(), InsecureIgnoreTlog: true})
```

- Credentials: `TokenSource` nil -> `google.FindDefaultCredentials(Scope)` per token refresh: `GOOGLE_APPLICATION_CREDENTIALS` (service account key, workload identity federation `external_account` incl. projected k8s token file), gcloud ADC file, GKE metadata server (Workload Identity). `TokenSource` set -> wrapped in `oauth2.ReuseTokenSource`.
- Key = full `cryptoKeyVersions/<n>` name: version pinned by config (asymmetric keys: no primary version). IDs start alnum; project allows `.`/`:` (domain-scoped); no `..` -> no escape from `/v1/`.
- `New`: validates config before I/O (`ErrInvalidConfig`), `GET v1/<key>/publicKey`; answer `name` = key, `pemCrc32c` match required (`ErrChecksum`); algorithm vs PEM key family/curve/size checked.
- Algorithms: `EC_SIGN_P256_SHA256`, `EC_SIGN_P384_SHA384`, `EC_SIGN_ED25519`, `RSA_SIGN_PSS_{2048,3072,4096}_SHA256`, `RSA_SIGN_PSS_4096_SHA512`, `RSA_SIGN_PKCS1_{2048,3072,4096}_SHA256`, `RSA_SIGN_PKCS1_4096_SHA512`. Raw PKCS #1, secp256k1, PQ -> `ErrUnsupportedKey`.
- `Sign(rand, digest, opts)`: rand unused. One algorithm per version: `opts.HashFunc()` = version hash, `*rsa.PSSOptions` iff PSS version (salt `Auto`, `EqualsHash` or hash size), digest length checked; Ed25519: hash 0, `data` field (pure), Ed25519ph/ctx rejected. Rejections `ErrUnsupportedHash` before I/O. sign.Image signs RSA with PKCS #1 v1.5 SHA-256: PSS and SHA-512 versions fail there.
- Integrity: request carries `digestCrc32c`/`dataCrc32c`; answer needs `verifiedDigestCrc32c`/`verifiedDataCrc32c` true, `signatureCrc32c` match (`ErrChecksum`), `name` = key; signature verified locally (PSS with caller's salt length).
- Timeout (HISS-02): `Config.Timeout` (default 30s) bounds `publicKey` and each `Sign` whole, ADC token refresh included: ADC token cached; refresh re-runs `google.FindDefaultCredentials` under request ctx (oauth2 token sources keep creation ctx). `Config.TokenSource` = no ctx (caller's bound); token requests also capped by `http.Client.Timeout`.
- No retries: Cloud KMS errors and checksum mismatches return to caller.
- Bounds: response <= 1 MiB.
- koanf tags on scalar fields; no fx module: stateless constructor (Hard Rule 3). JSON tags camelCase = REST wire format (`.golangci.yml` tagliatelle exclusion).

## IAM

`roles/cloudkms.signerVerifier` on key, or `cloudkms.cryptoKeyVersions.useToSign` + `cloudkms.cryptoKeyVersions.viewPublicKey`.

## Tests

- `gcp_test.go` + `fake_test.go`: httptest Cloud KMS REST fake (`getPublicKey`, `asymmetricSign`), real keys, real CRC32C.
  - Config: invalid table rejected before I/O (0 requests, 0 token calls); name boundaries (domain-scoped project, `_`/`-`, version 0); endpoint path prefix; https needs CA (`Transport`).
  - Sign: every algorithm/opts (digest field + CRC asserted, signature verified); bad opts before I/O; 8 concurrent signs (race); token reused (1 call / 3 signs).
  - Answers: 403/500/non-JSON, other version name, no name, CRC unverified / data CRC instead / signature CRC wrong / absent, bad base64, foreign signature, > 1 MiB, `ErrChecksum`; public key: name, pemCrc32c, raw PKCS #1, secp256k1, ML-DSA, family/curve/size mismatch, no PEM, bad DER; 404, token error.
  - Bounds: Timeout bounds `Sign` + `New`; 1ns valid but times out; answer 1 MiB ok, +1 rejected.
- `adc_test.go` (not parallel, `t.Setenv`): `GOOGLE_APPLICATION_CREDENTIALS` service account key -> fake token endpoint (JWT bearer grant); workload identity federation config with file subject token -> fake STS token exchange; token fetched once for 2 signs. Missing file, rejected subject token.
- `image_test.go`: `sign.Image` -> `sign.Verify` (ggcr in-process registry) for P-256, P-384, RSA PKCS #1 2048, Ed25519 via fake; foreign key -> `ErrNoValidSignature`; PSS version and denied sign -> `sign.Image` fails.
- Gap: no Cloud KMS emulator exists; fake follows REST reference (docs.cloud.google.com/kms/docs/reference/rest). GKE metadata server path not exercised.

## Notes

- Dependency choice (Hard Rule 2), measured `go list -m all` / go.sum, probe module per option: stdlib + `golang.org/x/oauth2` v0.37.0 3/4 clean; `cloud.google.com/go/kms` v1.35.0 (gRPC) 188/96; sigstore `pkg/signature/kms/gcp` v1.11.0 230/112. Both alternatives pull `golang.org/x/net` v0.58 + `x/crypto` v0.55 with govulncheck findings.
- Module graph weight = tests only: `sign` (e2e). No root golusoris require (vault notes apply).
