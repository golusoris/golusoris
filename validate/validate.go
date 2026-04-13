// Package validate wraps go-playground/validator with golusoris conventions:
// errors map to [errors.Validation] with a structured message describing
// failed fields by their JSON name.
//
// Apps inject [*Validator] via fx and call Struct/Var.
package validate

import (
	"errors"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
	"go.uber.org/fx"

	gerr "github.com/golusoris/golusoris/errors"
)

// Validator wraps *validator.Validate.
type Validator struct{ v *validator.Validate }

// New returns a Validator with sane defaults: tag name "validate", json-name
// extraction so error messages reference the wire field name not the Go
// field.
func New() *Validator {
	v := validator.New(validator.WithRequiredStructEnabled())

	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		if name == "" {
			return fld.Name
		}
		return name
	})

	return &Validator{v: v}
}

// Struct validates a struct using its `validate:"..."` tags. Returns nil on
// success, a [*gerr.Error] with [gerr.CodeValidation] on failure. The Cause
// is the underlying validator.ValidationErrors so callers can introspect.
func (v *Validator) Struct(s any) error {
	if err := v.v.Struct(s); err != nil {
		return gerr.Wrap(err, gerr.CodeValidation, formatErrors(err))
	}
	return nil
}

// Var validates a single value against a tag string. Same error semantics as
// Struct.
func (v *Validator) Var(value any, tag string) error {
	if err := v.v.Var(value, tag); err != nil {
		return gerr.Wrap(err, gerr.CodeValidation, formatErrors(err))
	}
	return nil
}

// Raw exposes the underlying *validator.Validate for advanced use (custom
// validator registration, etc.).
func (v *Validator) Raw() *validator.Validate { return v.v }

func formatErrors(err error) string {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return err.Error()
	}
	parts := make([]string, 0, len(ve))
	for _, fe := range ve {
		parts = append(parts, fe.Field()+": "+fe.Tag())
	}
	return strings.Join(parts, ", ")
}

// Module provides a *Validator via fx.
var Module = fx.Module("golusoris.validate",
	fx.Provide(New),
)
