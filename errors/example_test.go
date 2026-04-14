package errors_test

import (
	stderrors "errors"
	"fmt"

	"github.com/golusoris/golusoris/errors"
)

// ExampleNew shows the basic constructor flow.
func ExampleNew() {
	err := errors.New(errors.CodeNotFound, "user 42 not found")
	fmt.Println(err.Error(), err.Status())
	// Output: user 42 not found 404
}

// ExampleWrap shows wrapping a lower-level error with a code + message. The
// original error remains accessible via errors.Is / errors.As.
func ExampleWrap() {
	cause := stderrors.New("connection refused")
	err := errors.Wrap(cause, errors.CodeUnavailable, "postgres unreachable")
	fmt.Println(err.Status())
	fmt.Println(errors.Is(err, cause))
	// Output:
	// 503
	// true
}

// ExampleNotFound shows the convenience constructor.
func ExampleNotFound() {
	err := errors.NotFound("widget 7")
	fmt.Println(err.Status(), err.Code)
	// Output: 404 not_found
}
