<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/captcha

Verifies CAPTCHA tokens against Cloudflare Turnstile, hCaptcha, and Google reCAPTCHA.

## Surface

- `NewTurnstile(secret, client)` / `NewHCaptcha(...)`.
- `NewRecaptcha(secret, client)`: v2 only; rejects v3 score/action responses.
- `NewRecaptchaV3(secret, client, minimumScore, expectedAction)`: score plus exact action policy.
- `Verifier.Verify(ctx, token, remoteIP)` — returns `gerr.Unauthorized` on failure.

## Notes

- Same wire shape across providers: POST form (secret/response/remoteip) → JSON body with `success`.
- Non-2xx provider response always fails verification; response JSON cannot override transport status.
- reCAPTCHA v3 action: 1–256 ASCII alphanumeric, `/`, `_`; exact response match required.
- reCAPTCHA v3 score: finite `[0,1]`; response score must meet configured minimum.
- Inject `httpx/client` for retry / OTel instrumentation.
