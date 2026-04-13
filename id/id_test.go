package id_test

import (
	"testing"

	"github.com/golusoris/golusoris/id"
)

func TestNewUUIDIsV7(t *testing.T) {
	t.Parallel()
	g := id.New()
	u := g.NewUUID()
	if u.Version() != 7 {
		t.Errorf("UUID version = %d, want 7", u.Version())
	}
}

func TestKSUIDIsUnique(t *testing.T) {
	t.Parallel()
	g := id.New()
	a, b := g.NewKSUID(), g.NewKSUID()
	if a == b {
		t.Error("two consecutive KSUIDs collided")
	}
}
