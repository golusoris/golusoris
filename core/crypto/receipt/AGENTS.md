<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/crypto/receipt

> Security-relevant. Receipts are *proof of a passing gate*; never sign
> anything you did not run.

Ed25519-signed **Exit-0 execution receipts**: command, exit code (always 0),
UTC timestamp, commit SHA, repository, SHA-256 of the output, the signer's
public key, signature. Canonical payload is wire-compatible with praetor
`lockdown.ExecutionReceipt`. Capability key: `crypto.receipt`.

## Key API

| Symbol | Purpose |
|---|---|
| `GenerateKey()` | fresh Ed25519 key pair (crypto/rand) |
| `NewSigner(priv, clock)` · `NewSignerFromSeed(seed32, clock)` | signer; time is injected (`clock.Clock`), never `time.Now` |
| `(*Signer).Sign(Run)` | returns `*Receipt`; refuses `ExitCode != 0` (`ErrNonZeroExit`) and empty commands |
| `Verify(r)` · `VerifyOutput(r, output)` | signature check; plus output-hash check |
| `(*Receipt).Payload()` | canonical bytes: `version\ncommand\nexit\nRFC3339Nano\nsha\nrepo\nhash` |
| `Module` | fx-provides `*Signer` from `crypto.receipt.seed` (hex, 32 bytes); ephemeral key + WARN when unset |

## Config keys

```
crypto.receipt.seed   # hex-encoded 32-byte Ed25519 seed — QUOTE it in YAML (an all-digit hex
                      # string would otherwise parse as a number); store in secrets/, not in git
```

## Don't

- Don't sign failures. A failed gate produces no receipt — that is the signal.
- Don't change `Payload()` field order; it is the interop contract.
- Don't ship with the ephemeral fallback in production — nobody can verify it.
