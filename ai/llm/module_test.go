// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package llm

import (
	"math"
	"testing"

	"github.com/golusoris/golusoris/core/config"
)

func TestNewClientReturnsOpenAIClient(t *testing.T) {
	t.Parallel()
	c, err := newClient(Options{BaseURL: "http://localhost", APIKey: "k", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if _, ok := c.(*OpenAIClient); !ok {
		t.Fatalf("newClient = %T, want *OpenAIClient", c)
	}
}

func TestNewClientPropagatesHTTPBounds(t *testing.T) {
	t.Parallel()
	client, err := newClient(Options{MaxResponseBytes: math.MaxInt64})
	if err == nil {
		t.Fatal("unbounded response limit accepted")
	}
	if client != nil {
		t.Fatalf("client = %T; want nil after invalid configuration", client)
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
