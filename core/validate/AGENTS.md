<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — validate/

Wraps [go-playground/validator](https://github.com/go-playground/validator)
with golusoris conventions: failures map to `*errors.Error` with
`errors.CodeValidation` (→ HTTP 400), and messages reference **JSON** field
name (from `json` tag) rather than Go field name.

## Key API

| Symbol | Purpose |
| --- | --- |
| `validate.Module` | fx module — provides `*Validator` |
| `validate.New()` | build `*Validator` directly (tests) |
| `validate.IsNil(value)` | reject nil and typed-nil dependencies |
| `Validator.Struct(s)` | validate via `validate:"..."` tags |
| `Validator.Var(value, tag)` | validate single value against tag |
| `Validator.Raw()` | underlying `*validator.Validate` for custom rule registration |

On failure underlying `validator.ValidationErrors` is preserved as error `Cause`, so callers can `errors.As` it for field-level detail.

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
 `fx.Invoke`, not per request.
- Don't surface raw `Cause` to clients — formatted `Message` is  client-safe surface.
