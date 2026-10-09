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
// keyless: ephemeral P-256 key + Fulcio cert for OIDC token
sig, err = sign.Image(ctx, client, ref,
    sign.Signer{IDToken: func(ctx context.Context) (string, error) { return ciToken(ctx) }},
    sign.Options{FulcioURL: "https://fulcio.sigstore.dev", RekorURL: "https://rekor.sigstore.dev"})
// sig.Descriptor = referrer manifest, sig.Subject = signed manifest, sig.Bundle = bundle JSON
sigs, err := sign.Verify(ctx, client, ref, sign.Policy{Key: pub, InsecureIgnoreTlog: true}) // = cosign verify --key --insecure-ignore-tlog
tr, _ := root.FetchTrustedRoot() // sigstore-go TUF; or root.NewTrustedRootFromJSON
sigs, err = sign.Verify(ctx, client, ref, sign.Policy{TrustedMaterial: tr,
    Identities: []sign.Identity{{Issuer: "https://token.actions.githubusercontent.com", SubjectRegexp: `https://github\.com/org/app/\.github/workflows/.+`}}})
// sigs[i] = Signature (referrer, subject, bundle) + Result (*verify.VerificationResult)
// OCI image layout, offline: digest = v1.Hash of image/index in layout
l, err := registry.OpenLayout("/data/models/oci", registry.Options{})
sig, err = sign.ImageLayout(ctx, l, digest, sign.Signer{Key: kmsSigner}, sign.Options{})
sigs, err = sign.VerifyLayout(ctx, l, digest, sign.Policy{Key: pub, InsecureIgnoreTlog: true})
// keyless ID token sources = Signer.IDToken
sign.Signer{IDToken: sign.GitHubToken{}.Token}  // GitHub Actions job, permissions id-token: write
sign.Signer{IDToken: sign.FileToken{}.Token}    // projected SA token at DefaultTokenPath
```

- `Signer{Key}` -> public-key bundle. `Signer{IDToken}` -> keyless. Both -> Fulcio cert over caller key.
- `Options`: `FulcioURL` (with `IDToken`, both or neither), `RekorURL` + `RekorVersion` (0/1 = v1, 2 = v2; keyless v2 needs `TSAURL`), `TSAURL` (full RFC 3161 endpoint), `Annotations` (in-toto subject annotations = `cosign sign -a`), `Timeout` (default `DefaultTimeout` 2m, whole Sigstore exchange + each request), `Transport` (Fulcio + TSA; nil = private `http.DefaultTransport` clone, idle conns closed after), `Clock` (stamps `org.opencontainers.image.created`).
- koanf tags on scalar `Options` fields; no fx module: stateless function (Hard Rule 3).
- Sentinels: `ErrDigestRequired` (tag or bare repo), `ErrInvalidOptions` (signer/options/policy/key type), `ErrNoValidSignature` (no referrer bundle passes; wraps per-referrer reasons via `errors.Join`). First two checked before network I/O. Subject digest mismatch -> go-containerregistry error or `registry.ErrDigestMismatch`.

## Verify

- `Policy`: `Key` (`crypto.PublicKey`) xor `Identities` (keyless: `Issuer`/`IssuerRegexp` + `Subject`/`SubjectRegexp`; any identity matches; regexps anchored `^(?:re)$`, cosign = substring). `TrustedMaterial` (`root.TrustedMaterial`: Fulcio CAs, Rekor + CT logs, TSAs) required except `Key` + `InsecureIgnoreTlog` without `SignedTimestamps`. `Annotations` = `cosign verify -a`, string values on subject naming digest.
- Zero value = cosign default, fail closed: Rekor entry required (`InsecureIgnoreTlog` = `--insecure-ignore-tlog`), keyless SCT required (`InsecureIgnoreSCT` = `--insecure-ignore-sct`), `SignedTimestamps` = `--use-signed-timestamps`. Verifier options mapped like cosign v3.1.3 `CheckOpts.verificationOptions`: key -> no observer timestamps (or TSA); keyless -> log integrated time, TSA, or current time with tlog off.
- Per referrer: unfiltered `Client.Referrers` (ggcr quirk below); skip artifactType neither empty, `application/vnd.oci.empty.v1+json` nor `application/vnd.dev.sigstore.bundle*`; manifest = exactly 1 layer `application/vnd.dev.sigstore.bundle*`; bundle <= 1 MiB, >= v0.3 (cosign `ociremote.Bundle`); sigstore-go `Verify` with `WithArtifactDigest` = manifest digest; predicate type = `PredicateType` (stricter than cosign v3.1.3, which accepts any attestation: SBOM/provenance by same signer != image signature); DSSE enforced by same check (message signature -> no statement). Returns every passing bundle.
- Rekor v2 keyless bundles: set `SignedTimestamps` (cosign auto-detects v2-only entries; `Verify` no auto-detect). `Image` already forces TSA for keyless v2.
- Key in `Policy.Key` = only trusted key; keys inside `TrustedMaterial` never substitute.
- `Verify` reads no clock; sigstore-go `WithCurrentTime` (keyless, tlog off, no TSA) uses wall time.

## Layouts

- `ImageLayout` / `VerifyLayout` = `Image` / `Verify` over `*registry.Layout`. One code path: unexported `store` (`store.go`: `remoteStore`, `layoutStore`) -> same statement, bundle, referrer manifest, policy checks, sentinels. Referrer lands as manifest blob + `index.json` entry (`container/registry/AGENTS.md` layouts).
- Offline: `ImageLayout` with `Signer{Key}` and no Rekor/TSA = zero network. `VerifyLayout` with key policy + `InsecureIgnoreTlog`, or `TrustedMaterial` held in memory (`root.NewTrustedRootFromJSON`) = zero network.
- Carry: `registry.Client.CopyFromLayout` pushes image + signature -> `Verify` and `cosign verify` accept; `registry.Client.CopyToLayout` pulls registry signatures (own or cosign v3) -> `VerifyLayout` offline.
- Zero digest -> `ErrDigestRequired`; malformed digest -> `registry.ErrInvalidArtifact`; tampered subject -> `registry.ErrDigestMismatch`; tampered or foreign bundle -> `ErrNoValidSignature`.

## ID tokens

- `FileToken{Path}`: file re-read per call (kubelet rotates projected tokens), trimmed, <= 64 KiB. Empty `Path` -> `DefaultTokenPath` `/var/run/sigstore/cosign/oidc-token` = cosign v3.1.3 filesystem provider path -> pod specs written for cosign work unchanged. Pod spec sets audience: projected volume source `serviceAccountToken{audience: sigstore, expirationSeconds: 600, path: oidc-token}` mounted at `/var/run/sigstore/cosign`.
- `GitHubToken{Audience, Timeout, Transport}`: GET `$ACTIONS_ID_TOKEN_REQUEST_URL` + `audience` query (default `DefaultAudience` = `sigstore`), header `Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN`, reply `{"value": jwt}`; cosign v3.1.3 github provider shape. Env read per call. Bounded: `Timeout` (default `DefaultTokenTimeout` 30s; ctx + `http.Client.Timeout`), reply <= 64 KiB, no retry (cosign retries 3x; caller decides). Nil `Transport` -> private `http.DefaultTransport` clone, idle conns closed after.
- Static token: `func(context.Context) (string, error) { return tok, nil }`; no helper.
- Tokens + runtime bearer never logged, never inside errors (tests assert). Sentinels: `ErrNoToken` (unset env, blank token), `ErrInvalidOptions` (oversized token, non-http URL).

## Wire format (cosign v3.1.3 parity)

- Payload: in-toto Statement v1, sole subject `{digest: {sha256: <manifest hex>}, annotations}`, `predicateType` = `PredicateType` (`https://sigstore.dev/cosign/sign/v1`), empty predicate. Same as cosign `signDigestBundle`.
- Envelope: DSSE, payloadType `application/vnd.in-toto+json`; bundle = protobuf JSON, media type `BundleMediaType` (`application/vnd.dev.sigstore.bundle.v0.3+json`).
- Referrer: `registry.Client.PushArtifact`, artifactType + single layer `BundleMediaType`, empty config, subject = signed manifest descriptor, annotations `dev.sigstore.bundle.content=dsse-envelope`, `dev.sigstore.bundle.predicateType`, `org.opencontainers.image.created`. Same as cosign `WriteAttestationNewBundleFormat`, minus cosign's stray `artifactType` on config descriptor.
- Key pair adapter (`keypair.go`): default sigstore algorithm per key type (ECDSA P-256/P-384, RSA PKCS#1 v1.5, pure Ed25519), hint = base64(sha256(PKIX)) — cosign `SignerVerifierKeypair` shape.
- cosign verify: `cosign verify --key k.pub --insecure-ignore-tlog=true <ref@digest>` (no Rekor) or `--certificate-identity/--certificate-oidc-issuer` (keyless). In process: `Verify`.

## Tests

- `sign_test.go`: ggcr in-process registry (tag-schema fallback + referrers API), ECDSA P-256/P-384, RSA, Ed25519, image + index; bundle verified via sigstore-go with cosign `--key` options; referrer read back via `Client.Referrers`; rejects before I/O; signer, push, subject failures.
- `services_test.go`: httptest Fulcio (checks bearer token + proof of possession, issues code-signing cert), RFC 3161 TSA (digitorus/timestamp), one-entry Rekor v1 log (rekor canonicalization, real SET + inclusion proof + signed checkpoint). Keyless verified against identity with current time or signed timestamp.
- `layout_test.go`: `ImageLayout` on ggcr-written layouts (image + index, every key type) -> cosign `--key` checks + `VerifyLayout`; referrer manifest pinned field by field; layout -> registry (tag schema + referrers API) -> `Verify`; registry -> layout -> `VerifyLayout`; tampered bundle, tampered subject, foreign bundle, other key, unsigned; rejects before I/O.
- `token_test.go`: file rotation, size cap at + over, blank, missing, canceled ctx, default path; fake Actions runtime (bearer, `api-version` kept, audience), 403, 502 with token body, non-JSON, empty, oversized, hung runtime -> deadline; keyless `ImageLayout` via `GitHubToken` + test Fulcio -> offline `VerifyLayout` by identity.
- `verify_test.go`: `Image` -> `Verify` for key types, image + index, tag-schema + referrers API; keyless identities (exact, regexp, anchoring, any-of); default SCT/tlog requirements; TSA; Rekor log trusted vs foreign; annotations; crafted referrers (other digest, payload edited, other predicate, message signature, bundle v0.2, garbage, non-bundle layer); several referrers one valid; policy/ref rejected before I/O; canceled ctx. Mutation run: 30 mutants of `verify.go`, 29 killed, 1 equivalent (key: `WithCurrentTime` vs `WithNoObserverTimestamps`, key without validity window).

## Notes

- Own go.mod: sigstore-go graph = +282 modules over `container/registry` (116 -> 398). Non-signing registry consumers stay lean. Root + core gain nothing.
- `rekor >= v1.5.4` required: v1.5.3 links `golang.org/x/crypto/openpgp` (GO-2026-5932, no fix) via `rekor/pkg/pki/pgp` init. v1.5.4 uses ProtonMail/go-crypto. `grpc >= v1.83.1` (GO-2026-6348).
- sigstore-go v1.3.0 Fulcio and TSA requests carry no context: bounded by `Options.Timeout` per request (`http.Client.Timeout`), retries = 1, backoff waits honor ctx. Rekor v1 gets ctx.
- sigstore-go's Rekor clients ignore `Options.Transport`.
- `crypto.Signer.Sign` takes no ctx: KMS signer bounds own call.
- ggcr test registry reports `config.mediaType` as referrer artifactType -> `Verify` + tests list referrers unfiltered, like cosign `GetBundles`; `TestVerify_ReferrersAPIQuirk` pins quirk.
