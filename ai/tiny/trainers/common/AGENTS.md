<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/trainers/common

Framework-neutral Python container contract.

## Contract

- Input: `/work/input`; output: `/work/output`; scratch: `/work/tmp`.
- JSON config: one bounded regular file; exact allowed keys; finite numbers.
- Paths: fixed children only; no caller-controlled traversal.
- Writes: temporary regular file; flush; fsync; atomic replace.
- Metrics: bounded canonical JSON; no credentials or source records.
- Framework imports: forbidden here; trainer entrypoints own heavy runtimes.

## Change gate

- Parser change: positive, malformed, boundary, plus-one regressions.
- Run: `PYTHONPATH=ai/tiny python3 -B -m unittest trainers.common.test_contract`.
