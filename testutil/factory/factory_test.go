package factory_test

import (
	"testing"

	"github.com/golusoris/golusoris/testutil/factory"
)

func TestNew_deterministic(t *testing.T) {
	f1 := factory.New(t)
	f2 := factory.New(t)
	// Same test name → same seed → same sequence.
	if f1.Email() != f2.Email() {
		t.Fatal("expected deterministic output for same test name")
	}
}

func TestNew_producesValues(t *testing.T) {
	f := factory.New(t)
	if f.Email() == "" {
		t.Fatal("Email() returned empty string")
	}
	if f.Name() == "" {
		t.Fatal("Name() returned empty string")
	}
	if f.UUID() == "" {
		t.Fatal("UUID() returned empty string")
	}
}

func TestRandom_notNil(t *testing.T) {
	f := factory.Random()
	if f == nil {
		t.Fatal("Random() returned nil")
	}
	// Random faker produces values.
	if f.Email() == "" {
		t.Fatal("Email() returned empty string")
	}
}
