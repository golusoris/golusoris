// Package i18n is a thin wrapper around nicksnyder/go-i18n providing locale
// negotiation from HTTP Accept-Language and a per-request [Localizer].
//
// Apps load message catalogs at startup and resolve via [Bundle.LocalizerFor].
package i18n

import (
	"net/http"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"go.uber.org/fx"
	"golang.org/x/text/language"
)

// Bundle holds loaded message catalogs and a default language matcher.
type Bundle struct {
	b *i18n.Bundle
}

// New creates a bundle with the given default language. Apps then call
// [Bundle.LoadMessageFile] (etc.) to populate translations.
func New(defaultLang language.Tag) *Bundle {
	return &Bundle{b: i18n.NewBundle(defaultLang)}
}

// Raw exposes the underlying *i18n.Bundle for advanced use (custom unmarshal
// funcs, message loading from io.Reader, etc.).
func (b *Bundle) Raw() *i18n.Bundle { return b.b }

// LoadMessageFile loads a message catalog from disk (e.g. "active.de.toml").
func (b *Bundle) LoadMessageFile(path string) error {
	_, err := b.b.LoadMessageFile(path)
	return err //nolint:wrapcheck // already a typed error
}

// LocalizerFor builds a localizer from an Accept-Language header value plus
// optional explicit overrides (e.g. user preference).
func (b *Bundle) LocalizerFor(acceptLanguage string, prefs ...string) *i18n.Localizer {
	all := append([]string{}, prefs...)
	if acceptLanguage != "" {
		all = append(all, acceptLanguage)
	}
	return i18n.NewLocalizer(b.b, all...)
}

// LocalizerFromRequest is a convenience for HTTP handlers.
func (b *Bundle) LocalizerFromRequest(r *http.Request) *i18n.Localizer {
	return b.LocalizerFor(r.Header.Get("Accept-Language"))
}

// Module provides a default [*Bundle] (English default). Apps load catalogs
// in their own fx.Invoke after this module.
var Module = fx.Module("golusoris.i18n",
	fx.Provide(func() *Bundle { return New(language.English) }),
)
