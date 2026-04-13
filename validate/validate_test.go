package validate_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/errors"
	"github.com/golusoris/golusoris/validate"
)

type user struct {
	Name  string `json:"name"  validate:"required"`
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age"   validate:"gte=0,lte=130"`
}

func TestValidateOK(t *testing.T) {
	t.Parallel()
	v := validate.New()
	if err := v.Struct(user{Name: "x", Email: "x@y.com", Age: 5}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateFails(t *testing.T) {
	t.Parallel()
	v := validate.New()
	err := v.Struct(user{Email: "not-an-email", Age: -1})
	if err == nil {
		t.Fatal("expected error")
	}
	var ge *errors.Error
	if !errors.As(err, &ge) || ge.Code != errors.CodeValidation {
		t.Errorf("expected CodeValidation, got %+v", err)
	}
	// Error message should reference json field names, not Go field names.
	if !strings.Contains(err.Error(), "email") || !strings.Contains(err.Error(), "name") {
		t.Errorf("expected json field names in error: %s", err.Error())
	}
}
