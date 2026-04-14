package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestValOrDefault_zero(t *testing.T) {
	t.Parallel()
	if got := valOrDefault(0, 5*time.Second); got != 5*time.Second {
		t.Fatalf("want 5s, got %v", got)
	}
}

func TestValOrDefault_nonzero(t *testing.T) {
	t.Parallel()
	if got := valOrDefault(3*time.Second, 5*time.Second); got != 3*time.Second {
		t.Fatalf("want 3s, got %v", got)
	}
}

func TestValOrDefaultU32_zero(t *testing.T) {
	t.Parallel()
	if got := valOrDefaultU32(0, 1); got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

func TestValOrDefaultU32_nonzero(t *testing.T) {
	t.Parallel()
	if got := valOrDefaultU32(3, 1); got != 3 {
		t.Fatalf("want 3, got %d", got)
	}
}

func TestErrServerError(t *testing.T) {
	t.Parallel()
	err := errServerError(503)
	if err == nil {
		t.Fatal("want non-nil error")
	}
	if !errors.Is(err, errServerErrorSentinel) {
		t.Fatalf("want error to wrap errServerErrorSentinel, got %v", err)
	}
}

func TestNew_defaults(t *testing.T) {
	t.Parallel()
	c := New(Options{})
	if c == nil {
		t.Fatal("want non-nil *http.Client")
	}
}

func TestDrain_nil(t *testing.T) {
	t.Parallel()
	// must not panic
	Drain(context.Background(), nil)
}

func TestDrain_response(t *testing.T) {
	t.Parallel()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("hello"))}
	Drain(context.Background(), resp)
	// body should be fully drained; a subsequent read yields 0 bytes
	n, _ := resp.Body.Read(make([]byte, 16))
	if n != 0 {
		t.Fatalf("want 0 bytes after Drain, got %d", n)
	}
}
