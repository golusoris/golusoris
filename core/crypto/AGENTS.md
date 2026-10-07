<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — crypto/

> Security-relevant. Read threat-model notes below before using.

Small set of cryptographic primitives: argon2id password hashing, AES-GCM
symmetric encryption, and secure-random helpers. Stateless functions work directly; `crypto.Module` provides configured hasher and encryptor helpers.

## Key API

| Symbol | Purpose |
|---|---|
| `HashPassword(plain)` | argon2id hash with `DefaultPasswordParams`, returns PHC string |
| `HashPasswordWith(plain, *argon2id.Params)` | hash with custom params |
| `VerifyPassword(plain, hash)` | returns `(match, needsRehash, err)` |
| `DefaultPasswordParams` | 2026 argon2id defaults (~100ms/hash) |
| `Seal(key, plaintext)` | AES-GCM encrypt → `nonce‖ciphertext` |
| `Open(key, sealed)` | AES-GCM decrypt |
| `RandomBytes(n)` | n cryptographically-secure random bytes |
| `SecureToken(n)` | hex-encoded random token (2n chars) over `RandomBytes` |
| `NewPasswordHasher(maxConcurrent)` · `TryHash`/`Hash` | load-shed bounded argon2id; `TryHash` returns `ErrBusy` when saturated |
| `NewEncryptor(key)` · `Seal`/`Open` | fixed-key encryptor (no per-call key) |
| `Module` | fx-provides `*PasswordHasher` (`crypto.hasher.max_concurrent`, default GOMAXPROCS) + `*Encryptor` (`crypto.key`: required hex AES key) |
| `ErrBusy` · `ErrShortCiphertext` | hasher saturated · `Open` input too short |

## Threat model / usage caveats

- **Keys.** `Seal`/`Open` take raw 16/24/32-byte AES key — caller owns key
 derivation, storage, and rotation. Pull keys from `secrets/`, never hard-code
 or commit them. 256-bit key from `RandomBytes(32)` is expected input.
- **Nonce.** `Seal` generates fresh random nonce per call and prepends it.
 Never reuse (key, nonce) pair — that breaks GCM confidentiality. Don't
 construct nonces yourself; always go through `Seal`.
- **Message size.** AES-GCM has per-key data limit and 64 KiB-safe AAD/IV
 story only with random nonces; for very large or many messages, rotate keys.
 This package targets field/column-level secrets, not bulk streaming.
- `NewEncryptor` clones key bytes; later caller mutation cannot alter behavior.
- **Passwords.** `VerifyPassword` is constant-time via argon2id library.
 Honor `needsRehash` — when it returns true, rehash with `HashPassword` and
 persist, so params track `DefaultPasswordParams` over time.
- Higher-level password policy lives in `auth/policy/`.
- No key rotation, sealed-secret, or column-encryption layer ships here.

## Don't

- Don't log keys, plaintext, nonces, or password hashes — not even at debug.
- Don't reuse JWT signing secrets as encryption keys. Missing `crypto.key` fails construction.
- Don't reuse a key+nonce pair, and don't reimplement nonce generation.
- Don't compare password hashes with `==`; always use `VerifyPassword`.
- Don't roll your own cipher mode here — if AES-GCM doesn't fit, raise ADR.
