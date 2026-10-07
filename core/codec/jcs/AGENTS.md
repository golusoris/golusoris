<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/codec/jcs

RFC 8785 JSON Canonicalization Scheme (#632). Hash or sign JSON records
(provenance, result documents, attestations) so digest made in C, JS or Go
verifies everywhere. Stdlib only. Capability key: `json.canonical`.

## Key API

| Symbol | Purpose |
| --- | --- |
| `Canonicalize(data)` | canonical bytes: no whitespace, members sorted by UTF-16 code units, ECMAScript string + number form |
| `ErrDuplicateKey` | same member name twice (escaped spellings count) |
| `ErrLoneSurrogate` | `\uD800`-style escape without pair |
| `ErrInvalidUTF8` | invalid UTF-8 byte sequence, incl. encoded surrogates |
| `ErrNumberRange` | number overflows IEEE 754 double (`1e400`); underflow -> `0` like ECMAScript |
| `ErrSyntax` / `ErrTooDeep` | not RFC 8259 JSON / nesting over `MaxDepth` (10000, same as encoding/json) |

All errors wrap sentinel + byte offset; match with `errors.Is`.

## Rules

- Parser + emitter iterative (HISS-01), explicit stacks; one pass per byte, no per-level copying.
- Numbers: `strconv` shortest digits, fixed for 1e-6 <= |x| < 1e21, exponent without leading zero, `-0` -> `0`.
- No struct `Marshal` helper: encode with encoding/json, then `Canonicalize`.

## Tests

- `testdata/cyberphone/`: upstream vectors @ 19d51d7 (Apache-2.0) — 6 input/output pairs, ES6 number sequence seeds + published SHA-256 checkpoints.
- `TestES6NumberFile`: 100k lines (1M without `-short`) of es6testfile100m hashed against upstream checkpoints.
- RFC 8785 Appendix B table, sections 3.2.3 + 3.2.4 samples.
- `FuzzCanonicalize`: invalid JSON never accepted, fixed point, value preserved vs encoding/json.
