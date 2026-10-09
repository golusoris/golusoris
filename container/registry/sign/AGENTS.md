<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/sign/

In-process Sigstore signing of OCI manifests by digest over
[sigstore-go](https://github.com/sigstore/sigstore-go) `pkg/sign`. No cosign
binary, no exec (HISS-08). Output = layout `cosign sign` writes with new
bundle format (cosign v3 default) -> `cosign verify` accepts output.

## API

```go
sig, err := sign.Image(ctx, client, "ghcr.io/org/app@sha256:...", // *registry.Client; digest required
    sign.Signer{Key: kmsSigner},                                  // crypto.Signer: memory, PKCS#11, KMS
    sign.Options{RekorURL: "https://rekor.sigstore.dev"})         // optional log
// keyless: ephemeral P-256 key + Fulcio cert for OIDC token
sig, err = sign.Image(ctx, client, ref,
    sign.Signer{IDToken: func(ctx context.Context) (string, error) { return ciToken(ctx) }},
    sign.Options{FulcioURL: "https://fulcio.sigstore.dev", RekorURL: "https://rekor.sigstore.dev"})
// sig.Descriptor = referrer manifest, sig.Subject = signed manifest, sig.Bundle = bundle JSON
```

- `Signer{Key}` -> public-key bundle. `Signer{IDToken}` -> keyless. Both -> Fulcio cert over caller key.
- `Options`: `FulcioURL` (with `IDToken`, both or neither), `RekorURL` + `RekorVersion` (0/1 = v1, 2 = v2; keyless v2 needs `TSAURL`), `TSAURL` (full RFC 3161 endpoint), `Annotations` (in-toto subject annotations = `cosign sign -a`), `Timeout` (default `DefaultTimeout` 2m, whole Sigstore exchange + each request), `Transport` (Fulcio + TSA; nil = private `http.DefaultTransport` clone, idle conns closed after), `Clock` (stamps `org.opencontainers.image.created`).
- koanf tags on scalar `Options` fields; no fx module: stateless function (Hard Rule 3).
- Sentinels: `ErrDigestRequired` (tag or bare repo), `ErrInvalidOptions` (signer/options/key type). Both checked before network I/O. Subject digest mismatch -> go-containerregistry error or `registry.ErrDigestMismatch`.

## Wire format (cosign v3.1.3 parity)

- Payload: in-toto Statement v1, sole subject `{digest: {sha256: <manifest hex>}, annotations}`, `predicateType` = `PredicateType` (`https://sigstore.dev/cosign/sign/v1`), empty predicate. Same as cosign `signDigestBundle`.
- Envelope: DSSE, payloadType `application/vnd.in-toto+json`; bundle = protobuf JSON, media type `BundleMediaType` (`application/vnd.dev.sigstore.bundle.v0.3+json`).
- Referrer: `registry.Client.PushArtifact`, artifactType + single layer `BundleMediaType`, empty config, subject = signed manifest descriptor, annotations `dev.sigstore.bundle.content=dsse-envelope`, `dev.sigstore.bundle.predicateType`, `org.opencontainers.image.created`. Same as cosign `WriteAttestationNewBundleFormat`, minus cosign's stray `artifactType` on config descriptor.
- Key pair adapter (`keypair.go`): default sigstore algorithm per key type (ECDSA P-256/P-384, RSA PKCS#1 v1.5, pure Ed25519), hint = base64(sha256(PKIX)) — cosign `SignerVerifierKeypair` shape.
- Verify: `cosign verify --key k.pub --insecure-ignore-tlog=true <ref@digest>` (no Rekor) or `--certificate-identity/--certificate-oidc-issuer` (keyless). Tests replay same sigstore-go checks. No `Verify` helper here.

## Tests

- `sign_test.go`: ggcr in-process registry (tag-schema fallback + referrers API), ECDSA P-256/P-384, RSA, Ed25519, image + index; bundle verified via sigstore-go with cosign `--key` options; referrer read back via `Client.Referrers`; rejects before I/O; signer, push, subject failures.
- `services_test.go`: httptest Fulcio (checks bearer token + proof of possession, issues code-signing cert), RFC 3161 TSA (digitorus/timestamp), Rekor v1 stub. Keyless verified against identity with current time or signed timestamp.

## Notes

- Own go.mod: sigstore-go graph = +282 modules over `container/registry` (116 -> 398). Non-signing registry consumers stay lean. Root + core gain nothing.
- `rekor >= v1.5.4` required: v1.5.3 links `golang.org/x/crypto/openpgp` (GO-2026-5932, no fix) via `rekor/pkg/pki/pgp` init. v1.5.4 uses ProtonMail/go-crypto. `grpc >= v1.83.1` (GO-2026-6348).
- sigstore-go v1.3.0 Fulcio and TSA requests carry no context: bounded by `Options.Timeout` per request (`http.Client.Timeout`), retries = 1, backoff waits honor ctx. Rekor v1 gets ctx.
- sigstore-go's Rekor clients ignore `Options.Transport`.
- `crypto.Signer.Sign` takes no ctx: KMS signer bounds own call.
- ggcr test registry reports `config.mediaType` as referrer artifactType -> tests list referrers unfiltered, like cosign `GetBundles`.
