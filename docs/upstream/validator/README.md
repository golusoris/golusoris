# go-playground/validator/v10 — v10.26.0 snapshot

Pinned: **v10.26.0**
Source: https://pkg.go.dev/github.com/go-playground/validator/v10@v10.26.0

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
    for _, e := range errs {
        fmt.Printf("Field: %s, Tag: %s, Value: %v\n", e.Field(), e.Tag(), e.Value())
    }
}
```

## Common tags

```
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
validate.RegisterValidation("is-cool", func(fl validator.FieldLevel) bool {
    return fl.Field().String() == "cool"
})
```

## golusoris usage

- `validate/` — `*validator.Validate` singleton provided via fx; ogen request decode validation.

## Links

- Changelog: https://github.com/go-playground/validator/blob/master/CHANGELOG.md
- Baked-in validations: https://pkg.go.dev/github.com/go-playground/validator/v10#hdr-Baked_In_Validators_and_Tags
