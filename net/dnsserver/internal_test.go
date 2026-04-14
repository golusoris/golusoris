package dnsserver

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zero(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Addr != defaultAddr {
		t.Errorf("Addr = %q, want %q", c.Addr, defaultAddr)
	}
	if c.UDPSize != defaultUDPSize {
		t.Errorf("UDPSize = %d, want %d", c.UDPSize, defaultUDPSize)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	c := Config{Addr: ":5353", UDPSize: 512}.withDefaults()
	if c.Addr != ":5353" {
		t.Errorf("Addr = %q", c.Addr)
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
		t.Fatalf("loadConfig: %v", err)
	}
	if c.Addr != defaultAddr {
		t.Errorf("Addr = %q", c.Addr)
	}
}

func TestNewServeMux(t *testing.T) {
	t.Parallel()
	if newServeMux() == nil {
		t.Error("newServeMux returned nil")
	}
}
