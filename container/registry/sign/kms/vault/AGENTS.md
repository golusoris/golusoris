<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/sign/kms/vault/

`crypto.Signer` over HashiCorp Vault or OpenBao transit key: key never leaves server, signs for `sign.Image`. Own go.mod; non-test code = stdlib `net/http` only (no Vault SDK). Same transit + auth HTTP API on both servers.

## API

```go
s, err := vault.New(ctx, vault.Config{
    Address: "https://openbao.openbao.svc:8200", // http(s)://host[:port][/path]
    Key:     "image-signing",                     // transit key; Mount default "transit"
    Role:    "exports-signer",                    // SA-JWT login; AuthMount default "kubernetes"
    Transport: tlsTransport,                      // private CA roots; nil = http.DefaultTransport clone
})
sig, err := sign.Image(ctx, client, ref, sign.Signer{Key: s}, sign.Options{})
sigs, err := sign.Verify(ctx, client, ref, sign.Policy{Key: s.Public(), InsecureIgnoreTlog: true})
```

- Auth: exactly one of `Token` (token auth) or `Role`. `Role` -> POST `auth/<AuthMount>/login {role, jwt}`, JWT read from `JWTFile` (default `/var/run/secrets/kubernetes.io/serviceaccount/token`) at every login (kubelet rotation). `AuthMount: "jwt"` = JWT auth method validating SA tokens by issuer key/OIDC discovery: same request.
- Token lifetime: no clock, no lease tracking. 403 on sign under `Role` -> one re-login (file re-read) + one retry; second 403 returned. Concurrent denials share one login (`loginMu`, token compare). Token auth never re-logs in.
- `New`: validates config before I/O (`ErrInvalidConfig`), logs in (`Role`), GET `<Mount>/keys/<Key>`; pins `KeyVersion` (0 = `latest_version` at `New`) so `Public` stays fixed across rotation. Rejects `supports_signing: false`, `derived`, unknown types (`ErrUnsupportedKey`).
- Key types: `ecdsa-p256/p384/p521` (PKIX PEM), `rsa-2048/3072/4096` (PKIX PEM), `ed25519` (base64 raw 32 bytes). Type vs PEM key family mismatch -> `ErrUnsupportedKey`.
- `Sign(rand, digest, opts)`: rand unused. ECDSA/RSA: `prehashed: true`, path suffix `sha2-224|256|384|512` from `opts.HashFunc()`, digest length checked. RSA: `pkcs1v15`, `*rsa.PSSOptions` -> `pss` + `salt_length` (`auto`/`hash`/n). Ed25519: hash 0, digest = message, `prehashed: false` (pure; transit refuses Ed25519ph, OpenBao 2.7 silently ignores `prehashed`). Bad opts -> `ErrUnsupportedHash` before I/O.
- Answer `vault:v<N>:<base64>`: N = pinned version, else error; signature verified locally against pinned public key before return (rotated/swapped key fails here).
- Timeout (HISS-02): `Config.Timeout` (default `DefaultTimeout` 30s) bounds each `New` request and each `Sign` call whole (incl. re-login). `crypto.Signer.Sign` without ctx -> `context.Background` + Timeout. `http.Client.Timeout` = same value.
- Bounds: response <= 1 MiB, JWT file 1..64 KiB.
- Names: key = Vault pattern `\w(([\w-.@]+)?\w)?`; mounts = `/`-joined names (no `.`/`..`/empty segment -> no escape from `/v1/`). Address: no userinfo/query/fragment.
- koanf tags on scalar fields; no fx module: stateless constructor (Hard Rule 3).

## Server policy

```hcl
path "transit/keys/<key>" { capabilities = ["read"] }
path "transit/sign/<key>/*" { capabilities = ["update"] }
path "transit/sign/<key>" { capabilities = ["update"] }   # ed25519 (no hash suffix)
```

## Tests

- `vault_test.go` + `fake_test.go`: httptest transit fake, real keys.
  - Config: invalid table rejected before I/O (0 requests); name boundaries; address path prefix.
  - Sign: every key type/hash/padding (body + path asserted, signature verified); version pinning (0/1/2; 3 missing); bad opts before I/O.
  - Server answers: 5xx, wrong version, bad base64, foreign signature, no data, non-JSON, > 1 MiB.
  - Login: mounts default/`jwt`/nested; re-login once + JWT re-read; relogin failure joined; token auth no re-login; 8 concurrent denials -> 1 login (race); JWT file missing/empty/64 KiB/64 KiB+1.
  - Bounds: Timeout bounds `Sign` + `New`; unsupported keys.
- `image_test.go`: `sign.Image` -> `sign.Verify` (ggcr in-process registry) per key type via fake; foreign key -> `ErrNoValidSignature`; denied transit -> `sign.Image` fails.
- `server_test.go` (Docker; `internal/vaultdev`; skip on `-short`/no Docker): Vault 2.1.2 + OpenBao 2.7.1. Token auth (policy-scoped token) round trip per key type, rotation keeps pinned version, bad token 403. JWT auth method (ES256 SA token, `jwt_validation_pubkeys`), token TTL 2s waited out -> re-login proven, foreign audience 400. Kubernetes auth method: host network (Linux only, skip elsewhere) + httptest TLS TokenReview fake; accepted SA token round trip, rejected token 403.
- Gap: real kube-apiserver TokenReview not exercised (fake answers TokenReview; Vault/OpenBao k8s plugin real).

## Notes

- Dependency choice (Hard Rule 2), measured `go list -m all` / `go mod graph` / go.sum, probe module per option: stdlib 1/2/0; `hashicorp/vault/api` v1.23.0 51/114/59; `openbao/openbao/api/v2` v2.7.1 50/98/54; sigstore `pkg/signature/kms/hashivault` v1.11.0 113/258/87. vault/api, openbao api, sigstore pick `golang.org/x/net` v0.47 (GO-2026-6617/6612/6611 reachable until bumped). sigstore provider also: `init()` registration, no ctx on transit calls, token only from env/`~/.vault-token`, `log.Printf`.
- Module graph weight = tests only: `sign` (e2e) + testcontainers. Own graph 442 modules / 2051 edges / 513 go.sum lines. Consumer importing `vault`: build list = stdlib + this module; pruned graph 147 modules, go.sum 290 lines (go.mod hashes).
- No root golusoris require: root `testutil/*` + `internal/testimages` unusable here (consumer `go mod tidy` resolves test imports of deps against released root, package absent until next root release).

## internal/vaultdev

- `Start(t, vaultdev.Vault|vaultdev.OpenBao)` -> `Server{Address, Token, Flavor}`; mapped port; random per-test root token (gitleaks clean).
- `StartHostNetwork(t, flavor)`: host network, `127.0.0.1:<free port>` (dev cluster listener = port+1); server reaches test httptest servers (TokenReview fake). Linux only, skip elsewhere (Docker Desktop: no host networking).
- `srv.Write` / `srv.Read`: root-token API call, path without `/v1/`, `Response{Data, Auth}`; non-2xx fails test.
- Pins `images.go`: `VaultImage` (hashicorp/vault 2.1.2, BUSL test use), `OpenBaoImage` (openbao/openbao 2.7.1), `RyukImage` (reaper). Renovate test-image manager covers file; `scripts/ci/renovate-policy-test.py` expects 3 pins; `TestRyukMatchesRootPin` = lockstep with root `internal/testimages`. Absent from CI cache manifest (module sweep pulls).
- Ready = dev banner log + `sys/health` 200 (bounded poll). No startgate (root-internal): 4 boots max per run.
