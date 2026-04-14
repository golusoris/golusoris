# Agent guide — notify/

Unified notification dispatcher. `Notifier` holds a list of `Sender`
implementations and tries them in order (first success wins) or fans
out to all via `Multi`.

## Usage

```go
n := notify.New(logger,
    notify.WithSender(smtpSender),
    notify.WithSender(slackSender), // fallback
)
err := n.Send(ctx, notify.Message{
    To:      []string{"user@example.com"},
    Subject: "Order confirmed",
    HTML:    "<p>Your order #42 is confirmed.</p>",
    Text:    "Your order #42 is confirmed.",
})
```

## Senders

| Sender           | Constructor                  | Channel                    |
| ---------------- | ---------------------------- | -------------------------- |
| `SMTPSender`     | `notify.NewSMTPSender(opts)` | Email via SMTP (go-mail)   |
| `slack.Sender`   | `slack.NewSender(opts)`      | Slack incoming webhook     |
| `discord.Sender` | `discord.NewSender(opts)`    | Discord incoming webhook   |

More senders (Resend, Postmark, web-push) are added per app by
implementing `notify.Sender`. Pull requests welcome.

## Suppression

Use `notify/unsub` to check suppression before sending:

```go
if suppressed, _ := unsubSvc.IsSuppressed(ctx, email); suppressed {
    return nil
}
```

## Don't

- Don't use `Send` for transactional fan-out — use `Multi` instead.
- Don't build HTML directly in the Sender — templates live in the app,
  senders receive the rendered string.
