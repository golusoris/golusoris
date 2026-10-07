<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — net/smtpserver/

fx-wired **inbound** SMTP server over [emersion/go-smtp]. Apps supply `smtp.Backend` (implement `Backend`/`Session` directly, or use built-in
`HandlerBackend` that delivers each message to callback).

## Wiring

```go
fx.New(
    smtpserver.Module, // starts the listener
    fx.Provide(func() smtp.Backend {
        return smtpserver.NewHandlerBackend(func(env smtpserver.Envelope) error {
            // env.From, env.To, env.Data (raw RFC 5322 bytes)
            return nil
        })
    }),
)
```

- **Provides:** nothing (server is started via `fx.Invoke`).
- **Requires:** `*config.Config`, `gosmtp.Backend`, `*slog.Logger`.
- **Config prefix:** `smtp` (env `APP_SMTP_*`).

```
smtp.addr              # listen address (default: :2525)
smtp.domain            # EHLO domain (default: localhost)
smtp.max_message_bytes # default 10 MiB
smtp.max_recipients    # default 50
smtp.read_timeout      # per-command (default 60s)
smtp.write_timeout     # per-command (default 60s)
smtp.tls_cert_file     # PEM certificate; pair with tls_key_file
smtp.tls_key_file      # PEM private key
smtp.implicit_tls      # false = STARTTLS; true = implicit TLS
smtp.allow_insecure_auth # default false
```

## Why emersion/go-smtp

Maintained, minimal Backend/Session interface; pairs with `emersion/go-message`
for parsing. stdlib only sends (`net/smtp`), it has no server.

## Notes

- TLS certificate pair enables STARTTLS. `implicit_tls` selects TLS from connection start.
- Fx startup binds the listener synchronously; address conflicts fail application startup.
- AUTH before TLS disabled by default. Enable `allow_insecure_auth` only for isolated development listeners.
- This is receive-only MTA building block — no spool, no relay, no spam
 filtering. `MessageHandler` is called synchronously per message;  non-nil return rejects delivery.
