# Agent guide — validate/

Wraps [go-playground/validator](https://github.com/go-playground/validator)
with golusoris conventions: failures map to a `*errors.Error` with
`errors.CodeValidation` (→ HTTP 400), and messages reference the **JSON** field
name (from the `json` tag) rather than the Go field name.

## Key API

| Symbol | Purpose |
|---|---|
| `validate.Module` | fx module — provides `*Validator` |
| `validate.New()` | build a `*Validator` directly (tests) |
| `Validator.Struct(s)` | validate via `validate:"..."` tags |
| `Validator.Var(value, tag)` | validate a single value against a tag |
| `Validator.Raw()` | underlying `*validator.Validate` for custom rule registration |

On failure the underlying `validator.ValidationErrors` is preserved as the
error `Cause`, so callers can `errors.As` it for field-level detail.

## Usage

```go
type Signup struct {
    Email    string `json:"email"    validate:"required,email"`
    Password string `json:"password" validate:"required,min=8"`
}

func (h *Handler) signup(v *validate.Validator, in Signup) error {
    return v.Struct(in) // nil, or *errors.Error{Code: validation}
}
```

## Don't

- Don't import go-playground/validator directly in handlers — use `*Validator`
  so error codes + JSON field names stay consistent.
- Don't register custom validators ad hoc; do it once via `Raw()` at startup in
  an `fx.Invoke`, not per request.
- Don't surface the raw `Cause` to clients — the formatted `Message` is the
  client-safe surface.
