package llm

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestNewClientReturnsOpenAIClient(t *testing.T) {
	t.Parallel()
	c := newClient(Options{BaseURL: "http://localhost", APIKey: "k", Model: "gpt-4o"})
	if _, ok := c.(*OpenAIClient); !ok {
		t.Fatalf("newClient = %T, want *OpenAIClient", c)
	}
}

func TestLoadOptionsEmptyConfig(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.BaseURL != "" || opts.Model != "" {
		t.Errorf("expected zero-value defaults, got %+v", opts)
	}
}
