# Agent guide — i18n/

Thin wrapper around [nicksnyder/go-i18n](https://github.com/nicksnyder/go-i18n)
providing locale negotiation from the HTTP `Accept-Language` header and a
per-request `*i18n.Localizer`.

## Key API

| Symbol | Purpose |
|---|---|
| `i18n.Module` | fx module — provides a default `*Bundle` (English) |
| `i18n.New(defaultLang)` | build a `*Bundle` with an explicit default `language.Tag` |
| `Bundle.LoadMessageFile(path)` | load a catalog (e.g. `active.de.toml`) |
| `Bundle.LocalizerFor(accept, prefs...)` | build a localizer from header + overrides |
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

User preference wins over the header: `b.LocalizerFor(accept, user.Lang)`.

## Don't

- Don't load message files per request — `LoadMessageFile` is a startup step in
  an `fx.Invoke`; `Localizer`s are the per-request objects.
- Don't construct `i18n.NewBundle` / `i18n.NewLocalizer` directly — go through
  `*Bundle` so the default language and catalog set stay shared.
- Don't trust `Accept-Language` for anything but localization — it's
  client-controlled.
