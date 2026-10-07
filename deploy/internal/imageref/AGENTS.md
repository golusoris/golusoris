<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# deploy/internal/imageref — AGENTS.md

Purpose: one immutable application-image guard for deployment references.

Rules:

- Accept only non-empty repository plus lowercase 64-hex SHA-256 digest.
- Reject mutable tags without digest, whitespace, extra separators, other algorithms.
- Keep package internal to isolated `deploy` module.
