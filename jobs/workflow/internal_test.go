package workflow

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zeroFilled(t *testing.T) {
	t.Parallel()
	got := Config{}.withDefaults()
	d := DefaultConfig()
	if got.Host != d.Host {
		t.Errorf("Host = %q, want %q", got.Host, d.Host)
	}
	if got.Namespace != d.Namespace {
		t.Errorf("Namespace = %q, want %q", got.Namespace, d.Namespace)
	}
}

func TestWithDefaults_preservesNonZero(t *testing.T) {
	t.Parallel()
	in := Config{
		Host:      "temporal.example.com:7233",
		Namespace: "prod",
		TaskQueue: "my-queue",
	}
	got := in.withDefaults()
	if got.Host != "temporal.example.com:7233" {
		t.Errorf("Host = %q, want temporal.example.com:7233", got.Host)
	}
	if got.Namespace != "prod" {
		t.Errorf("Namespace = %q, want prod", got.Namespace)
	}
	if got.TaskQueue != "my-queue" {
		t.Errorf("TaskQueue = %q, want my-queue", got.TaskQueue)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "localhost:7233" {
		t.Errorf("Host = %q, want localhost:7233", c.Host)
	}
	if c.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", c.Namespace)
	}
}
