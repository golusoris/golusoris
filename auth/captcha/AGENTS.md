# auth/captcha

Verifies CAPTCHA tokens against Cloudflare Turnstile, hCaptcha, and Google reCAPTCHA.

## Surface

- `NewTurnstile(secret, client)` / `NewHCaptcha(...)` / `NewRecaptcha(...)`.
- `Verifier.Verify(ctx, token, remoteIP)` — returns `gerr.Unauthorized` on failure.

## Notes

- Same wire shape across providers: POST form (secret/response/remoteip) → JSON body with `success`.
- For reCAPTCHA v3 score gating, parse the response yourself or wrap this verifier.
- Inject `httpx/client` for retry / OTel instrumentation.
