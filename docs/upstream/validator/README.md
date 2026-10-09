<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# go-playground/validator/v10 — v10.30.5 snapshot

Pinned: **v10.30.5**
Source: [tagged source](https://github.com/go-playground/validator/tree/v10.30.5)

## Initialization

```go
import "github.com/go-playground/validator/v10"

validate := validator.New(validator.WithRequiredStructEnabled())
// Use field names in errors (not struct field names)
validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
    name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
    if name == "-" { return "" }
    return name
})
```

## Struct validation

```go
type User struct {
    Name     string `validate:"required,min=2,max=100"`
    Email    string `validate:"required,email"`
    Age      int    `validate:"gte=0,lte=130"`
    URL      string `validate:"omitempty,url"`
    Password string `validate:"required,min=8"`
}

err := validate.Struct(user)

// Error handling
var errs validator.ValidationErrors
if errors.As(err, &errs) {
    first := errs[0]
    return fmt.Errorf("validate user: field %s failed %s", first.Field(), first.Tag())
}
if err != nil {
    return fmt.Errorf("validate user: %w", err)
}
```

## Common tags

```text
required        — field must be set (non-zero)
omitempty       — skip validation if zero value
min=N / max=N   — length or value bounds
gte=N / lte=N   — numeric bounds
email           — valid email address
url / uri       — valid URL / URI
uuid4           — UUID v4
len=N           — exact length
oneof=a b c     — must be one of listed values
alphanum        — alphanumeric only
numeric         — numeric string
gt=0            — greater than 0
dive            — validate slice/map elements
```

## Custom validators

```go
err := validate.RegisterValidation("is-cool", func(
    fl validator.FieldLevel,
) bool {
    return fl.Field().String() == "cool"
})
if err != nil {
    return fmt.Errorf("register is-cool validator: %w", err)
}
```

## golusoris usage

- `core/validate/` — `*validator.Validate` singleton provided via fx; ogen
  request decode validation.

## Links

- [Package documentation](https://pkg.go.dev/github.com/go-playground/validator/v10@v10.30.5)
- [Baked-in validations](https://pkg.go.dev/github.com/go-playground/validator/v10@v10.30.5#hdr-Baked_In_Validators_and_Tags)
