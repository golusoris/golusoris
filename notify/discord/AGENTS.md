# notify/discord

Discord incoming-webhook sender for `notify.Sender`.

## Surface

- `discord.NewSender(Options)` → `*Sender` implementing `notify.Sender`.
- `Options{WebhookURL, Username, AvatarURL, HTTPClient}`.

## Notes

- Uses Discord's incoming webhook API: POST JSON to the webhook URL.
- `notify.Message.Body` becomes Discord `content`. If empty, falls back to `**Subject**\nText` so the same `Message` works for email + Discord.
- Discord rate-limits per webhook (≈30/min). Pair with `httpx/ratelimit` if you fan out many messages.
- No SDK — raw HTTP only.
