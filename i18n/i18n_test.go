package i18n_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/text/language"

	"github.com/golusoris/golusoris/i18n"
)

func TestNewBundle(t *testing.T) {
	t.Parallel()
	b := i18n.New(language.English)
	if b.Raw() == nil {
		t.Error("Raw() returned nil")
	}
}

func TestLocalizerFromRequest(t *testing.T) {
	t.Parallel()
	b := i18n.New(language.English)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")
	if loc := b.LocalizerFromRequest(r); loc == nil {
		t.Error("LocalizerFromRequest returned nil")
	}
}
