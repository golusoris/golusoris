# auth/policy

Password-policy validator: minimum length, zxcvbn strength score, optional HaveIBeenPwned k-anonymity breach check.

## Surface

- `policy.New(opts)` → `*Policy`.
- `Validate(ctx, password, userInputs...)` — returns `gerr.Validation` on failure.
- `Score(password, userInputs...)` — raw zxcvbn score (0–4).

## Notes

- HIBP uses sha1 by API contract — `gosec` is suppressed locally with a justification.
- `userInputs` are penalised by zxcvbn (e.g. pass username + email so the password can't trivially contain them).
- `MaxBreachCount > 0` allows a configurable threshold; default 0 rejects any breach.
