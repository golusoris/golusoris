<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/gcs/

Google Cloud Storage backend: `storage.Bucket` + `storage.Copier` +
`storage.PutPresigner`. Own `go.mod` (cloud.google.com/go/storage pulls
gRPC xDS, Envoy control plane, SPIFFE, Cloud Monitoring; root graph stays
lean). Replaces `../..` + `../../core`.

## Wiring

```go
fx.New(golusoris.Core, gcs.Module) // provides storage.Bucket; replaces storage.Module
```

Needs `*config.Config`, `*slog.Logger`, `clock.Clock`. Construction bounded
by 15s; OnStop closes client.

```ini
storage.gcs.bucket       = "uploads"
storage.gcs.endpoint     = ""      # emulator JSON API base; empty = Google
storage.gcs.signer_email = ""      # SA that signs URLs; default detected
storage.gcs.presign_ttl  = 15m     # URL() GET lifetime, max 7d
storage.gcs.chunk_size   = 16777216  # 256 KiB..1 GiB resumable chunk
```

## Credentials + signing

- Auth: Application Default Credentials (GKE Workload Identity, workload
  identity federation, SA key file via `GOOGLE_APPLICATION_CREDENTIALS`).
  No static-key config.
- Signer: SA JSON with `private_key` -> local RSA signing. Keyless creds ->
  IAM Credentials `signBlob` (role `roles/iam.serviceAccountTokenCreator`
  on signer SA). Signer email: `signer_email` > JSON `client_email` /
  impersonation URL > metadata server default SA (cached after first
  success, failures retried).
- Own signBlob call (not library default): bounded by caller ctx + 30s.
- `endpoint` set -> unauthenticated client + ephemeral RSA key; emulators
  never verify signatures.
- Expiry: lib truncates `X-Goog-Expires` to whole seconds; 500ms pad keeps
  it = ttl. `PresignedRequest.Expires` = `X-Goog-Date` + `X-Goog-Expires`.

## Semantics

- `Put`: resumable upload, `chunk_size` buffer, auto CRC32C. Unseekable +
  unknown length OK. Body read error cancels upload.
- `Get`: `Attrs` then reader pinned to that generation (metadata matches body).
- `Copy`: rewrite pinned `GenerationMatch` on source; keeps content type +
  metadata; large objects loop rewrite tokens inside library.
- `List`: one bounded page, `MaxSize` = limit; attr selection Name/Size/Etag/Updated.
- `PresignPut`: V4 signed PUT. Content type, `x-goog-meta-*` (lowercased),
  exact length via `x-goog-content-length-range: N,N`, MD5 (`Content-MD5`)
  or CRC32C (`x-goog-hash`) = signed headers. SHA-256 ->
  `ErrUnsupportedChecksum`.

## Tests

- `internal_test.go`: option bounds, header mapping, IAM signBlob via
  httptest, email lookup caching, credential JSON parsing, TTL second rounding.
- `gcs_test.go`: `testutil/objstore.StartGCS` (fake-gcs-server) +
  `RunConformance` (`URLFetchable`; emulator enforces no signatures) +
  multi-chunk unseekable upload.
