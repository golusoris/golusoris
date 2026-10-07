<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — notify/

Unified notification dispatcher. `Notifier` holds list of `Sender`
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

| Sender            | Constructor                  | Channel                              |
| ----------------- | ---------------------------- | ------------------------------------ |
| `SMTPSender`      | `notify.NewSMTPSender(opts)` | Email via SMTP (go-mail)             |
| `resend.Sender`   | `resend.NewSender(opts)`     | Resend transactional email (HTTP)    |
| `postmark.Sender` | `postmark.NewSender(opts)`   | Postmark transactional email (HTTP)  |
| `slack.Sender`    | `slack.NewSender(opts)`      | Slack incoming webhook               |
| `discord.Sender`  | `discord.NewSender(opts)`    | Discord incoming webhook             |

More senders (Mailgun, SendGrid, Twilio, FCM, APNs, web-push, Telegram,
Teams, Gotify, ntfy) ship as additional `notify/<provider>` subpackages.

Outbound clients: clone caller client. Preserve positive timeout. Replace zero
timeout with 10s provider bound; APNs uses 15s.

## Suppression

Use `notify/unsub` to check suppression before sending:

```go
if suppressed, _ := unsubSvc.IsSuppressed(ctx, email); suppressed {
    return nil
}
```

## Don't

- Don't use `Send` for transactional fan-out — use `Multi` instead.
- Don't build HTML directly in Sender — templates live in app,
 senders receive rendered string.
