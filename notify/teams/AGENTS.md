<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/teams

Microsoft Teams sender for `notify.Sender` — emits MessageCard to Teams incoming webhook (legacy connector or Power Automate / Workflow).

## Surface

- `teams.NewSender(Options)` → `*Sender`.
- `Options{WebhookURL, ThemeColor, HTTPClient}`.

## Notes

- Raw HTTP — no SDK. POSTs MessageCard JSON document to webhook
 URL.
- `msg.Subject` → MessageCard `title`; `msg.Body` (fallback
 `msg.Text`, then `msg.HTML`) → `text`. `summary` falls back to  first line of text when no subject is set.
- Teams' legacy connector wire format uses `@type` / `@context` /
 `themeColor` — tagliatelle's snake-case rule doesn't apply. This
 package carries linter exception via file-level struct JSON
 tags; add to `.golangci.yml` exclusions if tagliatelle starts
 complaining.
- Microsoft announced legacy connector will be sunset in favour of
 Workflow URLs, but MessageCard payloads remain supported by  Workflow endpoint; no migration is needed for message format.
