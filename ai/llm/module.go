// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package llm

import (
	"fmt"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

// Options configures the ai/llm fx module — an OpenAI-compatible HTTP client.
// Config key prefix: ai.llm.* (works with OpenAI, Azure OpenAI, Ollama, Groq,
// Mistral, LM Studio, …). For native Anthropic/Ollama clients use the
// ai/llm/anthropic and ai/llm/ollama subpackages directly.
type Options struct {
	BaseURL             string        `koanf:"base_url"`
	APIKey              string        `koanf:"api_key"`
	Model               string        `koanf:"model"`
	EmbedModel          string        `koanf:"embed_model"`
	Timeout             time.Duration `koanf:"timeout"`
	MaxResponseBytes    int64         `koanf:"max_response_bytes"`
	MaxErrorBytes       int64         `koanf:"max_error_bytes"`
	MaxStreamFrameBytes int           `koanf:"max_stream_frame_bytes"`
}

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("ai.llm", &opts); err != nil {
		return Options{}, fmt.Errorf("ai/llm: load options: %w", err)
	}
	return opts, nil
}

func newClient(opts Options) (Client, error) {
	client, err := NewOpenAIClient(Config{
		BaseURL: opts.BaseURL, APIKey: opts.APIKey, Model: opts.Model,
		EmbedModel: opts.EmbedModel, Timeout: opts.Timeout,
		MaxResponseBytes: opts.MaxResponseBytes, MaxErrorBytes: opts.MaxErrorBytes,
		MaxStreamFrameBytes: opts.MaxStreamFrameBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("ai/llm: create client: %w", err)
	}
	return client, nil
}

// Module provides an OpenAI-compatible ai/llm.Client built from config.
//
//	fx.New(golusoris.Core, llm.Module) // provides llm.Client
//
// Requires [config] (via golusoris.Core). Config key prefix: ai.llm.*.
var Module = fx.Module(
	"golusoris.ai.llm",
	fx.Provide(loadOptions),
	fx.Provide(newClient),
)
