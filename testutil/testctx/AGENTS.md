<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/testctx/

Standard-library test utility; no fx wiring.

- `Deadline(t testing.TB, d time.Duration)`: `t.Context()` + timeout; `t.Cleanup(cancel)`.
- Positive duration required; `d <= 0` -> `t.Fatalf`.
- Tests: deadline bounds, cleanup cancellation, invalid durations, tiny-duration expiry.
