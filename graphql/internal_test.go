package graphql

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zeroFillsDefaults(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Path != defaultPath {
		t.Errorf("Path = %q, want %q", c.Path, defaultPath)
	}
	if c.PlaygroundPath != defaultPlaygroundPath {
		t.Errorf("PlaygroundPath = %q, want %q", c.PlaygroundPath, defaultPlaygroundPath)
	}
	if c.ComplexityLimit != defaultComplexity {
		t.Errorf("ComplexityLimit = %d, want %d", c.ComplexityLimit, defaultComplexity)
	}
	if c.APQCache != defaultAPQCache {
		t.Errorf("APQCache = %d, want %d", c.APQCache, defaultAPQCache)
	}
}

func TestWithDefaults_preservesExisting(t *testing.T) {
	t.Parallel()
	c := Config{Path: "/my", PlaygroundPath: "/play", ComplexityLimit: 50, APQCache: 10}.withDefaults()
	if c.Path != "/my" {
		t.Errorf("Path = %q, want /my", c.Path)
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
	if c.Path != defaultPath {
		t.Errorf("Path = %q", c.Path)
	}
	if !c.Playground {
		t.Error("Playground should default to true")
	}
}
