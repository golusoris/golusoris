package validate_test

import (
	"fmt"

	"github.com/golusoris/golusoris/errors"
	"github.com/golusoris/golusoris/validate"
)

type Signup struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

// ExampleValidator_Struct shows the happy-path and the failure-path with the
// JSON field name surfaced in the golusoris error message (the underlying
// validator detail is preserved as the error Cause but not shown here).
func ExampleValidator_Struct() {
	v := validate.New()

	if err := v.Struct(Signup{Email: "x@y.com", Password: "hunter22"}); err != nil {
		fmt.Println("ok-case unexpected error:", err)
		return
	}

	err := v.Struct(Signup{Email: "nope", Password: "x"})
	var ge *errors.Error
	errors.As(err, &ge)
	fmt.Println(ge.Code, "->", ge.Message)
	// Output: validation -> email: email, password: min
}
