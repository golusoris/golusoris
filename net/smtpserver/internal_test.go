package smtpserver

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
	if c.Domain != defaultDomain {
		t.Errorf("Domain = %q, want %q", c.Domain, defaultDomain)
	}
	if c.MaxMessageBytes != defaultMaxMessageBytes {
		t.Errorf("MaxMessageBytes = %d", c.MaxMessageBytes)
	}
	if c.MaxRecipients != defaultMaxRecipients {
		t.Errorf("MaxRecipients = %d", c.MaxRecipients)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	c := Config{Addr: ":2525", Domain: "example.com", MaxRecipients: 5}.withDefaults()
	if c.Addr != ":2525" {
		t.Errorf("Addr = %q", c.Addr)
	}
	if c.MaxRecipients != 5 {
		t.Errorf("MaxRecipients = %d", c.MaxRecipients)
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
