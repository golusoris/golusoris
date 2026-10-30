<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/sign/

In-process Sigstore signing + verification of OCI manifests by digest over
[sigstore-go](https://github.com/sigstore/sigstore-go) `pkg/sign` + `pkg/verify`.
No cosign binary, no exec (HISS-08). Output = layout `cosign sign` writes with
new bundle format (cosign v3 default) -> `cosign verify` accepts output;
`Verify` reads same layout back (own or cosign v3 signatures).

## API

```go
sig, err := sign.Image(ctx, client, "ghcr.io/org/app@sha256:...", // *registry.Client; digest required
    sign.Signer{Key: kmsSigner},                                  // crypto.Signer: memory, PKCS#11, KMS
    sign.Options{RekorURL: "https://rekor.sigstore.dev"})         // optional log
```

## Hard rules

- `Signer{Key}` -> public-key bundle. `Signer{IDToken}` -> keyless. Both -> Fulcio cert over caller key.
- koanf tags on scalar `Options` fields; no fx module: stateless function (Hard Rule 3).
- Sentinels: `ErrDigestRequired` (tag or bare repo), `ErrInvalidOptions` (signer/options/policy/key type), `ErrNoValidSignature` (no referrer bundle passes; wraps per-referrer reasons via `errors.Join`). First two checked before network I/O. Subject digest mismatch -> go-containerregistry error or `registry.ErrDigestMismatch`.
- Own go.mod: sigstore-go graph = +282 modules over `container/registry` (116 -> 398). Non-signing registry consumers stay lean. Root + core gain nothing.
- `rekor >= v1.5.4` required: v1.5.3 links `golang.org/x/crypto/openpgp` (GO-2026-5932, no fix) via `rekor/pkg/pki/pgp` init. v1.5.4 uses ProtonMail/go-crypto. `grpc >= v1.83.1` (GO-2026-6348).

Full API, wire format and notes: [REFERENCE.md](REFERENCE.md). Read it before changing this package.
