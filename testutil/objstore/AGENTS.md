<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/objstore/

Object-storage emulators via testcontainers + shared `storage.Bucket`
conformance suite. Docker required; `-short` or unhealthy Docker -> skip.

## Emulators

| Func | Image (`internal/testimages`) | Returns |
| --- | --- | --- |
| `StartS3(t)` | `VersityGW` (posix gateway, enforces SigV4 + presign expiry) | `S3Server{Endpoint, Region, AccessKey, SecretKey, Bucket}`; bucket pre-created; path-style only |
| `StartGCS(t)` | `FakeGCSServer` (no signature/expiry checks) | `GCSServer{Endpoint, Bucket}`; `Endpoint` = JSON API base for `option.WithEndpoint`; public host + external URL set to mapped port (XML reads, signed URLs, resumable sessions) |

Fresh container per call; `t.Cleanup` terminates. Boots queue through
`testutil/internal/startgate`. MinIO images no longer published -> VersityGW.

## Conformance

```go
objstore.RunConformance(t, bucket, objstore.Capabilities{
    PresignEnforced:        true, // server rejects expired / retargeted URLs
    PresignHeadersEnforced: true, // server rejects differing signed headers
    URLFetchable:           true, // URL() serves body over HTTP
})
```

Subtests: round trip (content type + metadata), empty object, stat/exists/
delete idempotence, missing key -> `ErrNotFound`, list prefix + limit bounds,
unsafe keys -> `ErrUnsafeKey`. `Copier` -> copy keeps attributes, missing
source, same key, unsafe key. `PutPresigner` -> upload via plain HTTP client +
`Get` readback, TTL bounds, invalid options; enforced caps add other-key,
expired (sleeps ~2.5s), header-mismatch rejection.
Keys use disjoint prefixes (`roundtrip/`, `list/`, `presign/` ...); one
bucket per `RunConformance` call.

## Don't

- Don't set a capability flag the emulator does not enforce — subtest fails.
- Don't add mutable image strings; pin in `internal/testimages` + CI cache
  manifest (Renovate owns updates).
