package crypto

import (
	"context"
	"errors"
	"testing"
)

func TestTryHashSucceedsWhenFree(t *testing.T) {
	t.Parallel()
	h := NewPasswordHasher(1)
	hash, err := h.TryHash("correct horse battery staple")
	if err != nil {
		t.Fatalf("TryHash: %v", err)
	}
	if match, _, _ := VerifyPassword("correct horse battery staple", hash); !match {
		t.Error("produced hash does not verify")
	}
}

func TestTryHashShedsWhenSaturated(t *testing.T) {
	t.Parallel()
	h := NewPasswordHasher(1)
	h.sem <- struct{}{} // occupy the only slot
	defer func() { <-h.sem }()

	if _, err := h.TryHash("pw"); !errors.Is(err, ErrBusy) {
		t.Fatalf("saturated TryHash = %v, want ErrBusy", err)
	}
}

func TestHashRespectsContextCancellation(t *testing.T) {
	t.Parallel()
	h := NewPasswordHasher(1)
	h.sem <- struct{}{} // occupy the only slot so Hash must wait
	defer func() { <-h.sem }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Hash(ctx, "pw"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hash on cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestNewPasswordHasherClampsConcurrency(t *testing.T) {
	t.Parallel()
	if got := cap(NewPasswordHasher(0).sem); got != 1 {
		t.Errorf("NewPasswordHasher(0) capacity = %d, want 1", got)
	}
	if got := cap(NewPasswordHasher(8).sem); got != 8 {
		t.Errorf("NewPasswordHasher(8) capacity = %d, want 8", got)
	}
}
