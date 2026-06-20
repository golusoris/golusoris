# Agent guide — net/smtpserver/

fx-wired **inbound** SMTP server over [emersion/go-smtp]. Apps supply a
`smtp.Backend` (implement `Backend`/`Session` directly, or use the built-in
`HandlerBackend` that delivers each message to a callback).

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

- **Provides:** nothing (the server is started via `fx.Invoke`).
- **Requires:** `*config.Config`, `gosmtp.Backend`, `*slog.Logger`.
- **Config prefix:** `smtp` (env `APP_SMTP_*`).

```
smtp.addr              # listen address (default: :2525)
smtp.domain            # EHLO domain (default: localhost)
smtp.max_message_bytes # default 10 MiB
smtp.max_recipients    # default 50
smtp.read_timeout      # per-command (default 60s)
smtp.write_timeout     # per-command (default 60s)
```

## Why emersion/go-smtp

Maintained, minimal Backend/Session interface; pairs with `emersion/go-message`
for parsing. stdlib only sends (`net/smtp`), it has no server.

## Notes

- `AllowInsecureAuth = true` — TLS is **opt-in** via the server's `TLSConfig`;
  do not expose plaintext AUTH on an untrusted network.
- This is a receive-only MTA building block — no spool, no relay, no spam
  filtering. The `MessageHandler` is called synchronously per message; a
  non-nil return rejects delivery.
