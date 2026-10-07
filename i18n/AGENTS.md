<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — i18n/

Thin wrapper around [nicksnyder/go-i18n](https://github.com/nicksnyder/go-i18n)
providing locale negotiation from HTTP `Accept-Language` header and per-request `*i18n.Localizer`.

## Key API

| Symbol | Purpose |
|---|---|
| `i18n.Module` | fx module — provides default `*Bundle` (English) |
| `i18n.New(defaultLang)` | build `*Bundle` with explicit default `language.Tag` |
| `Bundle.LoadMessageFile(path)` | load catalog (e.g. `active.de.toml`) |
| `Bundle.LocalizerFor(accept, prefs...)` | build localizer from header + overrides |
| `Bundle.LocalizerFromRequest(r)` | convenience for HTTP handlers |
| `Bundle.Raw()` | underlying `*i18n.Bundle` for advanced loading |

## Usage

```go
fx.New(i18n.Module, fx.Invoke(func(b *i18n.Bundle) {
    _ = b.LoadMessageFile("locales/active.de.toml") // load catalogs at startup
}))

func (h *Handler) greet(b *i18n.Bundle, r *http.Request) string {
    loc := b.LocalizerFromRequest(r)
    msg, _ := loc.Localize(&i18n.LocalizeConfig{MessageID: "greeting"})
    return msg
}
```

User preference wins over header: `b.LocalizerFor(accept, user.Lang)`.

## Don't

- Don't load message files per request — `LoadMessageFile` is startup step in
 `fx.Invoke`; `Localizer`s are per-request objects.
- Don't construct `i18n.NewBundle` / `i18n.NewLocalizer` directly — go through
 `*Bundle` so default language and catalog set stay shared.
- Don't trust `Accept-Language` for anything but localization — it's
 client-controlled.
