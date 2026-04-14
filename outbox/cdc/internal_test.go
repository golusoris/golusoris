package cdc

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zero(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Table != defaultTable {
		t.Errorf("Table = %q, want %q", c.Table, defaultTable)
	}
	if c.Schema != defaultSchema {
		t.Errorf("Schema = %q, want %q", c.Schema, defaultSchema)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	c := Config{Table: "custom_outbox", Schema: "myschema"}.withDefaults()
	if c.Table != "custom_outbox" {
		t.Errorf("Table = %q, want %q", c.Table, "custom_outbox")
	}
	if c.Schema != "myschema" {
		t.Errorf("Schema = %q, want %q", c.Schema, "myschema")
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.Table != defaultTable {
		t.Errorf("Table = %q, want %q", c.Table, defaultTable)
	}
	if c.Schema != defaultSchema {
		t.Errorf("Schema = %q, want %q", c.Schema, defaultSchema)
	}
}
