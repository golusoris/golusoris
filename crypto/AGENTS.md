# Agent guide — crypto/

> Security-relevant. Read the threat-model notes below before using.

Small set of cryptographic primitives: argon2id password hashing, AES-GCM
symmetric encryption, and secure-random helpers. Stateless toolkit — call the
package functions directly; `crypto.Module` provides nothing (it exists only so
`golusoris.Core` can include it for symmetry).

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
| `ErrShortCiphertext` | `Open` input too short to hold a nonce |

## Threat model / usage caveats

- **Keys.** `Seal`/`Open` take a raw 16/24/32-byte AES key — caller owns key
  derivation, storage, and rotation. Pull keys from `secrets/`, never hard-code
  or commit them. A 256-bit key from `RandomBytes(32)` is the expected input.
- **Nonce.** `Seal` generates a fresh random nonce per call and prepends it.
  Never reuse a (key, nonce) pair — that breaks GCM confidentiality. Don't
  construct nonces yourself; always go through `Seal`.
- **Message size.** AES-GCM has a per-key data limit and a 64 KiB-safe AAD/IV
  story only with random nonces; for very large or many messages, rotate keys.
  This package targets field/column-level secrets, not bulk streaming.
- **Passwords.** `VerifyPassword` is constant-time via the argon2id library.
  Honor `needsRehash` — when it returns true, rehash with `HashPassword` and
  persist, so params track `DefaultPasswordParams` over time.
- **Higher-level policy** (breach checks, complexity rules) lives in
  `auth/policy/`, not here. Field/column encryption helpers are planned in
  `crypto/columnenc/`.

## Don't

- Don't log keys, plaintext, nonces, or password hashes — not even at debug.
- Don't reuse a key+nonce pair, and don't reimplement nonce generation.
- Don't compare password hashes with `==`; always use `VerifyPassword`.
- Don't roll your own cipher mode here — if AES-GCM doesn't fit, raise an ADR.
