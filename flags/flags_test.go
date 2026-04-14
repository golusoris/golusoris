package flags_test

import (
	"context"
	"testing"

	"github.com/golusoris/golusoris/flags"
)

func TestBool(t *testing.T) {
	t.Parallel()
	p := flags.NewMemoryProvider()
	p.Set("feature-x", true)
	c := flags.New(p)

	if !c.Bool(context.Background(), "feature-x", false) {
		t.Fatal("expected true")
	}
	if c.Bool(context.Background(), "missing", false) {
		t.Fatal("missing key should return default (false)")
	}
}

func TestString(t *testing.T) {
	t.Parallel()
	p := flags.NewMemoryProvider()
	p.Set("api-version", "v2")
	c := flags.New(p)

	if got := c.String(context.Background(), "api-version", "v1"); got != "v2" {
		t.Fatalf("expected v2, got %q", got)
	}
	if got := c.String(context.Background(), "missing", "v1"); got != "v1" {
		t.Fatalf("expected default v1, got %q", got)
	}
}

func TestInt(t *testing.T) {
	t.Parallel()
	p := flags.NewMemoryProvider()
	p.Set("max-retries", int64(5))
	c := flags.New(p)

	if got := c.Int(context.Background(), "max-retries", 3); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}

func TestFloat(t *testing.T) {
	t.Parallel()
	p := flags.NewMemoryProvider()
	p.Set("rollout-pct", 0.25)
	c := flags.New(p)

	if got := c.Float(context.Background(), "rollout-pct", 0.0); got != 0.25 {
		t.Fatalf("expected 0.25, got %f", got)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	p := flags.NewMemoryProvider()
	p.Set("temp", true)
	p.Delete("temp")
	c := flags.New(p)

	if c.Bool(context.Background(), "temp", false) {
		t.Fatal("deleted flag should return default")
	}
}

func TestNoopProvider(t *testing.T) {
	t.Parallel()
	c := flags.New(flags.NoopProvider{})
	if c.Bool(context.Background(), "anything", true) != true {
		t.Fatal("noop should return default")
	}
}

func TestTypeMismatch_returnsDefault(t *testing.T) {
	t.Parallel()
	p := flags.NewMemoryProvider()
	p.Set("flag", "not-a-bool") // string stored, bool requested
	c := flags.New(p)

	if c.Bool(context.Background(), "flag", false) {
		t.Fatal("type mismatch should return default")
	}
}
