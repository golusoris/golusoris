// Package form decodes HTML form submissions into Go structs. Thin wrapper
// over go-playground/form/v4 with golusoris error semantics: decode
// failures become [*gerr.Error] with [gerr.CodeBadRequest].
//
// Usage in a handler:
//
//	var req SignupRequest
//	if err := r.ParseForm(); err != nil { ... }
//	if err := dec.Decode(&req, r.PostForm); err != nil { ... }
package form

import (
	"fmt"
	"net/url"

	gpform "github.com/go-playground/form/v4"
	"go.uber.org/fx"

	gerr "github.com/golusoris/golusoris/errors"
)

// Decoder wraps *gpform.Decoder.
type Decoder struct{ d *gpform.Decoder }

// New returns a Decoder with go-playground's default tag ("form").
func New() *Decoder { return &Decoder{d: gpform.NewDecoder()} }

// Decode populates dst from src using `form:"..."` struct tags.
func (d *Decoder) Decode(dst any, src url.Values) error {
	if err := d.d.Decode(dst, src); err != nil {
		return gerr.Wrap(err, gerr.CodeBadRequest, fmt.Sprintf("form decode: %s", err))
	}
	return nil
}

// Raw exposes the underlying decoder for advanced configuration (custom type
// decoders, tag overrides).
func (d *Decoder) Raw() *gpform.Decoder { return d.d }

// Module provides a *Decoder.
var Module = fx.Module("golusoris.httpx.form",
	fx.Provide(New),
)
