package outbox

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
)

// ---------------------------------------------------------------------------
// outbox.go — marshalPayload
// ---------------------------------------------------------------------------

func TestMarshalPayload_string(t *testing.T) {
	t.Parallel()
	raw, err := marshalPayload("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil bytes")
	}
}

func TestMarshalPayload_struct(t *testing.T) {
	t.Parallel()
	raw, err := marshalPayload(struct{ X int }{3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil bytes")
	}
}

func TestMarshalPayload_unmarshalable(t *testing.T) {
	t.Parallel()
	_, err := marshalPayload(make(chan int))
	if err == nil {
		t.Fatal("expected error for channel payload, got nil")
	}
}

// ---------------------------------------------------------------------------
// drainer.go — DefaultDrainerOptions / loadDrainerOptions
// ---------------------------------------------------------------------------

func TestDefaultDrainerOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultDrainerOptions()
	if opts.Interval != time.Second {
		t.Errorf("expected Interval=1s, got %v", opts.Interval)
	}
	if opts.Batch != 100 {
		t.Errorf("expected Batch=100, got %d", opts.Batch)
	}
}

func TestLoadDrainerOptions(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := loadDrainerOptions(cfg)
	if err != nil {
		t.Fatalf("loadDrainerOptions: %v", err)
	}
	// Empty config should return defaults.
	if opts.Interval != time.Second {
		t.Errorf("expected Interval=1s, got %v", opts.Interval)
	}
	if opts.Batch != 100 {
		t.Errorf("expected Batch=100, got %d", opts.Batch)
	}
}
