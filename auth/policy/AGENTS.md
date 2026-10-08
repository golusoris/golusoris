<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/policy

Password-policy validator: minimum length, zxcvbn strength score, optional HaveIBeenPwned k-anonymity breach check.

## Surface

- `policy.New(opts)` → `(*Policy, error)`; rejects invalid lengths, scores, and breach thresholds.
- `Validate(ctx, password, userInputs...)` — returns `gerr.Validation` on failure.
- `Score(password, userInputs...)` — raw zxcvbn score (0–4).
- `MinLength` = Unicode code points; invalid UTF-8 rejected.
- `DisableStrengthCheck` = explicit zero score; non-zero `MinScore` conflict rejected.

## Notes

- HIBP uses sha1 by API contract — `gosec` is suppressed locally with justification.
- HIBP client always has finite timeout; response body is capped at 1 MiB and overflow fails closed.
- `userInputs` are penalised by zxcvbn (e.g. pass username + email so password can't trivially contain them).
- `MaxBreachCount > 0` allows configurable threshold; default 0 rejects any breach.
