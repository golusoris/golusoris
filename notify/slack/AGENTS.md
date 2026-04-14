# notify/slack

Slack incoming-webhook sender for `notify.Sender`.

## Surface

- `slack.NewSender(Options)` → `*Sender`.
- `Options{WebhookURL, Channel, Username, IconEmoji, HTTPClient}`.

## Notes

- Slack incoming webhook (mrkdwn syntax). `notify.Message.Body` is the `text`. Falls back to `*Subject*\nText`.
- For Block Kit / interactive UIs use the upstream `slack-go/slack` SDK directly; this sender targets simple webhook delivery.
- Slack rate-limits per-workspace at ≈1 msg/sec for incoming webhooks; pair with `httpx/ratelimit` for high-volume use.
