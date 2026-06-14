# notify/gotify

Gotify (self-hosted push server) sender for `notify.Sender`.

## Surface

- `gotify.NewSender(Options)` → `*Sender` implementing `notify.Sender`.
- `Options{ServerURL, AppToken, Priority, HTTPClient}`.

## Notes

- POSTs JSON `{title, message, priority}` to `{ServerURL}/message?token={AppToken}`.
- `notify.Message.Body` becomes `message`, falling back to `Text`. `Subject` becomes `title`.
- Priority defaults to `Options.Priority`; `Message.Metadata["priority"]` (integer) overrides per-send. Gotify range is 0–10.
- `ServerURL` and `AppToken` are both required (validated in `NewSender`).
- No SDK — raw HTTP only.
