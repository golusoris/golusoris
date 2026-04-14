# notify/teams

Microsoft Teams sender for `notify.Sender` — emits a MessageCard to a
Teams incoming webhook (legacy connector or Power Automate / Workflow).

## Surface

- `teams.NewSender(Options)` → `*Sender`.
- `Options{WebhookURL, ThemeColor, HTTPClient}`.

## Notes

- Raw HTTP — no SDK. POSTs a MessageCard JSON document to the webhook
  URL.
- `msg.Subject` → MessageCard `title`; `msg.Body` (fallback
  `msg.Text`, then `msg.HTML`) → `text`. `summary` falls back to the
  first line of the text when no subject is set.
- Teams' legacy connector wire format uses `@type` / `@context` /
  `themeColor` — tagliatelle's snake-case rule doesn't apply. This
  package carries a linter exception via the file-level struct JSON
  tags; add to `tools/golangci.yml` exclusions if tagliatelle starts
  complaining.
- Microsoft announced the legacy connector will be sunset in favour of
  Workflow URLs, but MessageCard payloads remain supported by the
  Workflow endpoint; no migration is needed for message format.
