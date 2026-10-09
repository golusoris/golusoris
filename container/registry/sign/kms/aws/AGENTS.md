<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/sign/kms/aws/

`crypto.Signer` over AWS KMS asymmetric key: key never leaves KMS, signs for `sign.Image`. Own go.mod; non-test code = `aws-sdk-go-v2` `service/kms` + `config` only.

## API

```go
s, err := aws.New(ctx, aws.Config{
    Key: "alias/image-signing", // key ID, key ARN, alias name or alias ARN
    // Region: "eu-central-1",  // empty: key ARN region, else default chain (AWS_REGION)
    // Endpoint: "https://vpce-...kms.eu-central-1.vpce.amazonaws.com",
    // AWS: &awsCfg,            // nil: config.LoadDefaultConfig
})
sig, err := sign.Image(ctx, client, ref, sign.Signer{Key: s}, sign.Options{})
sigs, err := sign.Verify(ctx, client, ref, sign.Policy{Key: s.Public(), InsecureIgnoreTlog: true})
```

- Credentials: `Config.AWS` nil -> `config.LoadDefaultConfig`: env keys, shared files, IRSA (`AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE`), EKS Pod Identity (`AWS_CONTAINER_CREDENTIALS_FULL_URI` + token file), IMDS. `Config.Region` passed as `config.WithRegion` (web identity STS client built while loading). Set `Config.AWS` -> copied, used as is (credentials, retryer, HTTP client).
- Region: key/alias ARN region wins; `Config.Region` set and different -> `ErrInvalidConfig`. No region anywhere -> `ErrInvalidConfig`.
- `New`: validates config before I/O (`ErrInvalidConfig`), `GetPublicKey`, pins answered key ARN (alias moved later -> signer keeps old key, `Public` fixed). Answer ARN = same ARN, `:key/<id>` suffix for key ID, any key ARN for alias. Rejects usage != `SIGN_VERIFY`, empty algorithm list, key spec vs SPKI mismatch, curves Go lacks (secp256k1), SM2, ML-DSA (`ErrUnsupportedKey`).
- Key specs: `ECC_NIST_P256/P384/P521` (DER ECDSA), `RSA_2048/3072/4096`, `ECC_NIST_EDWARDS25519`.
- `Sign(rand, digest, opts)`: rand unused. ECDSA/RSA: `MessageType DIGEST`, algorithm from `opts.HashFunc()` (SHA-256/384/512), digest length checked. RSA: PKCS #1 v1.5, `*rsa.PSSOptions` -> `RSASSA_PSS_*`; salt `Auto`, `EqualsHash` or hash size only (KMS salts with hash length). Ed25519: hash 0, `ED25519_SHA_512`, `MessageType RAW`, message <= 4096 bytes; Ed25519ph/ctx rejected. Algorithm absent from key's `SigningAlgorithms` -> rejected. All rejections `ErrUnsupportedHash` before I/O.
- Answer: `KeyId` = pinned ARN, `SigningAlgorithm` = requested; signature verified locally against pinned public key (PSS with caller's salt length) before return.
- Timeout (HISS-02): `Config.Timeout` (default `DefaultTimeout` 30s) bounds `LoadDefaultConfig`, `GetPublicKey`, each `Sign` whole (SDK retries + credential refresh run under same ctx). `crypto.Signer.Sign` without ctx -> `context.Background` + Timeout.
- koanf tags on scalar fields; no fx module: stateless constructor (Hard Rule 3).

## IAM policy

```json
{"Effect": "Allow", "Action": ["kms:GetPublicKey", "kms:Sign"], "Resource": "arn:aws:kms:<region>:<account>:key/<id>"}
```

## Tests

- `aws_test.go` + `fake_test.go`: httptest KMS JSON-protocol fake (`TrentService.*`), real keys.
  - Config: invalid table rejected before I/O (0 requests); key forms (ID, ARN, alias, alias ARN, 2048-byte alias, `mrk-`); region resolution via SigV4 scope.
  - Sign: every spec/hash/padding (request algorithm + message type asserted, signature verified); Ed25519 message 0/1/4096 ok, 4097 rejected; bad opts before I/O; algorithm missing from key list; alias moved -> pinned key; 8 concurrent signs (race).
  - Answers: denied, 500, other key/algorithm, no key ID, foreign/no signature, non-JSON; foreign `GetPublicKey` ARN; unsupported keys (usage, specs, secp256k1 SPKI, bad DER, swapped key -> first Sign fails).
  - Bounds: Timeout bounds `Sign` + `New`; 1ns valid but times out.
- `chain_test.go` (not parallel, `t.Setenv`): default chain with env keys, IRSA against httptest STS (`AWS_ENDPOINT_URL_STS`), EKS Pod Identity agent fake; no credentials, rejected web identity token, no region.
- `image_test.go`: `sign.Image` -> `sign.Verify` (ggcr in-process registry) per spec via fake; foreign key -> `ErrNoValidSignature`; denied KMS -> `sign.Image` fails.
- `server_test.go` (Docker; `internal/localstackdev`; skip on `-short`/no Docker): LocalStack 4.14.0 KMS + STS. Image round trip per P-256/384/521/RSA-2048 key, PSS SHA-256/512, alias moved after `New` keeps pinned key, encrypt key + secp256k1 rejected, missing alias, disabled key. `TestServer_IRSA`: role ARN + token file exchanged at LocalStack STS.
- Gaps: LocalStack 4.14 lacks `ECC_NIST_EDWARDS25519` (Ed25519 = fake only); LocalStack resolves alias targets given as key ID only (test uses key ID); LocalStack skips SigV4 and IAM policy checks.

## Notes

- Dependency choice (Hard Rule 2), measured `go list -m all` / go.sum, probe module per option: aws-sdk-go-v2 `service/kms` v1.61.3 + `config` v1.33.8 16/30, govulncheck clean; sigstore `pkg/signature/kms/aws` v1.11.0 91/66, `golang.org/x/crypto` v0.55 findings, `init()` registration.
- Module graph weight = tests only: `sign` (e2e) + testcontainers. No root golusoris require (vault notes apply).

## internal/localstackdev

- `Start(t)` -> `Server{Endpoint, Region}`; mapped port; `SERVICES=kms,sts`; ready = `Ready.` log + `/_localstack/health` kms+sts available (bounded poll).
- Pins `images.go`: `LocalStackImage` (localstack/localstack 4.14.0), `RyukImage`. LocalStack 2026.03+ needs auth token: `renovate.json` holds `localstack/localstack` < 2026. Renovate test-image manager covers file; `scripts/ci/renovate-policy-test.py` expects 2 pins; `TestRyukMatchesRootPin` = lockstep with root `internal/testimages`.
