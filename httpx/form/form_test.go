package form_test

import (
	"errors"
	"net/url"
	"testing"

	gerr "github.com/golusoris/golusoris/errors"
	"github.com/golusoris/golusoris/httpx/form"
)

type signup struct {
	Email    string `form:"email"`
	Username string `form:"username"`
	Age      int    `form:"age"`
}

func TestDecode(t *testing.T) {
	t.Parallel()
	dec := form.New()
	values := url.Values{
		"email":    {"alice@example.test"},
		"username": {"alice"},
		"age":      {"34"},
	}
	var got signup
	if err := dec.Decode(&got, values); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Email != "alice@example.test" || got.Username != "alice" || got.Age != 34 {
		t.Errorf("got %+v", got)
	}
}

func TestRaw(t *testing.T) {
	t.Parallel()
	dec := form.New()
	raw := dec.Raw()
	if raw == nil {
		t.Fatal("Raw() returned nil")
	}
}

func TestDecodeInvalidWrapsAsBadRequest(t *testing.T) {
	t.Parallel()
	dec := form.New()
	values := url.Values{"age": {"not-a-number"}}
	var got signup
	err := dec.Decode(&got, values)
	if err == nil {
		t.Fatal("expected error")
	}
	var ge *gerr.Error
	if !errors.As(err, &ge) {
		t.Fatalf("expected *gerr.Error, got %T", err)
	}
	if ge.Code != gerr.CodeBadRequest {
		t.Errorf("Code = %s", ge.Code)
	}
}
