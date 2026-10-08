<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — hash/

Content-hashing helpers. Pick right hash for task:

| Function | Algorithm | Use when |
| --- | --- | --- |
| `SHA256(data)` | SHA-256 | Content dedup, integrity checks, security-sensitive |
| `BLAKE3(data)` | BLAKE3 | Fast cryptographic hash (3× faster than SHA-256) |
| `XX64(data)` | xxhash-64 | Non-cryptographic: caching keys, fast dedup, Bloom filters |
| `ETag(data)` | SHA-1 (quoted) | HTTP ETag headers (RFC 7232) |
| `HMACSHA256(secret, data)` | HMAC-SHA256 | Keyed token/credential hashing (magic links, recovery codes, API keys) |
| `ValidateHMACSHA256Key(secret)` | HMAC-SHA256 policy | Reject credential keys shorter than 32 bytes |
| `*Reader` variants | — | Streaming hash without loading full content into memory |
| `SHA256File(path)` | SHA-256 | Hashing files on disk |

## Don't

- Don't use `XX64` where security matters — it's not collision-resistant.
- Don't use `ETag` as security signature — SHA-1 is broken for that.
- Don't store BLAKE3 output in same column as SHA-256 — different lengths.
