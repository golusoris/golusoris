package cdc

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_slot(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Slot != defaultSlot {
		t.Errorf("Slot = %q, want %q", c.Slot, defaultSlot)
	}
}

func TestWithDefaults_publication(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Publication != defaultPublisher {
		t.Errorf("Publication = %q, want %q", c.Publication, defaultPublisher)
	}
}

func TestWithDefaults_standbyHz(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.StandbyHz != defaultStandbyHz {
		t.Errorf("StandbyHz = %d, want %d", c.StandbyHz, defaultStandbyHz)
	}
}

func TestWithDefaults_preservesExisting(t *testing.T) {
	t.Parallel()
	c := Config{Slot: "myslot", Publication: "mypub", StandbyHz: 5}.withDefaults()
	if c.Slot != "myslot" {
		t.Errorf("Slot = %q, want \"myslot\"", c.Slot)
	}
	if c.Publication != "mypub" {
		t.Errorf("Publication = %q, want \"mypub\"", c.Publication)
	}
	if c.StandbyHz != 5 {
		t.Errorf("StandbyHz = %d, want 5", c.StandbyHz)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_CDC_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Slot != defaultSlot {
		t.Errorf("Slot = %q, want %q", c.Slot, defaultSlot)
	}
	if c.Publication != defaultPublisher {
		t.Errorf("Publication = %q, want %q", c.Publication, defaultPublisher)
	}
	if c.StandbyHz != defaultStandbyHz {
		t.Errorf("StandbyHz = %d, want %d", c.StandbyHz, defaultStandbyHz)
	}
}
