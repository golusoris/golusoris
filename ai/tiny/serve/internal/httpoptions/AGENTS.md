<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/serve/internal/httpoptions — AGENTS.md

Purpose: one option-normalization path for Tiny HTTP predictors.

Rules:

- Preserve explicit endpoint, positive response cap, positive client timeout.
- Default empty endpoint and non-positive bounds from adapter contract.
- Clone injected client. Never mutate caller-owned client.
- Keep package internal to `ai/tiny/serve`.
