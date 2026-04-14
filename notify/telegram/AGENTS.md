# notify/telegram

Telegram Bot API sender for `notify.Sender`. Intended for ops/alert
channels via a bot placed in the target chat.

## Surface

- `telegram.NewSender(Options)` → `*Sender`.
- `Options{BotToken, ChatID, ParseMode, DisableWebPagePreview,
  DisableNotification, Endpoint, HTTPClient}`.
- `ParseModeNone` / `ParseModeHTML` / `ParseModeMarkdownV2` constants.

## Notes

- Raw HTTP — no SDK. POSTs JSON to
  `{endpoint}/bot{token}/sendMessage`.
- Chat resolution: `msg.To[0]` overrides `Options.ChatID` per-send.
  Accepted formats: numeric chat ID (e.g. `-1001234567890`) or
  `@channel_username` for public channels.
- Body resolution: `msg.Body` first, else `msg.Subject + "\n\n" + msg.Text`.
- When `ParseMode` is set to `HTML` or `MarkdownV2`, the body must
  already be safely escaped for that mode — the sender passes through
  as-is. Use `html.EscapeString` or a MarkdownV2 escaper before
  handing text to the sender.
- Attachments/photos/docs are not wired — this sender is messages-only.
  Use a dedicated media method when needed.
