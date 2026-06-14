# notify/ntfy

ntfy (ntfy.sh / self-hosted pub-sub push) sender for `notify.Sender`.

## Surface

- `ntfy.NewSender(Options)` → `*Sender` implementing `notify.Sender`.
- `Options{ServerURL, Topic, Priority, Tags, Token, Username, Password, HTTPClient}`.

## Notes

- POSTs the message as the raw HTTP body to `{ServerURL}/{Topic}`, with `Title`, `Priority`, and `Tags` headers.
- `notify.Message.Body` is the body, falling back to `Text`. `Subject` becomes the `Title` header.
- Priority (ntfy range 1–5) defaults to `Options.Priority`; `Message.Metadata["priority"]` (integer) overrides. Tags default to `Options.Tags`; `Message.Metadata["tags"]` (comma-separated) overrides.
- `Message.Metadata["click"]` sets the `Click` header (click-action URL); `Message.Metadata["icon"]` sets the `Icon` header. Both are per-send only (no Options default).
- Auth is optional: `Token` (bearer) takes precedence over `Username`/`Password` (basic).
- `ServerURL` and `Topic` are both required (validated in `NewSender`).
- No SDK — raw HTTP only.
