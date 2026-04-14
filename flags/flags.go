// Package flags provides typed feature-flag evaluation backed by a pluggable
// [Provider]. A [Client] exposes Bool/String/Int/Float methods. The MemoryProvider
// ships for testing; production deployments should back the provider with
// Postgres, Redis, or LaunchDarkly.
//
// The API surface mirrors OpenFeature's evaluation contract
// (https://openfeature.dev) so migrating to the official OpenFeature Go SDK
// is straightforward once needed.
//
// Usage:
//
//	p := flags.NewMemoryProvider()
//	p.Set("dark-mode", true)
//	p.Set("api-version", "v2")
//
//	client := flags.New(p)
//	enabled := client.Bool(ctx, "dark-mode", false)
package flags

import (
	"context"
	"fmt"
	"sync"
)

// EvalContext carries arbitrary attributes used by providers to target
// evaluations (user ID, tenant ID, region, …).
type EvalContext map[string]any

// Provider resolves flag values. The generic Evaluate method returns the raw
// value; typed helpers on [Client] coerce it.
type Provider interface {
	// Metadata returns a human-readable name for the provider.
	Metadata() ProviderMetadata
	// Evaluate resolves flag by key. Returns defaultValue when the flag is
	// unknown or disabled.
	Evaluate(ctx context.Context, key string, defaultValue any, evalCtx EvalContext) (any, error)
}

// ProviderMetadata describes a provider implementation.
type ProviderMetadata struct {
	Name string
}

// Client evaluates feature flags via the configured [Provider].
type Client struct {
	provider Provider
}

// New returns a Client backed by provider.
func New(provider Provider) *Client {
	return &Client{provider: provider}
}

// Bool evaluates flag key as a boolean. Returns defaultValue on error or
// when the flag is not defined.
func (c *Client) Bool(ctx context.Context, key string, defaultValue bool, evalCtx ...EvalContext) bool {
	ec := mergeCtx(evalCtx)
	v, err := c.provider.Evaluate(ctx, key, defaultValue, ec)
	if err != nil {
		return defaultValue
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return defaultValue
}

// String evaluates flag key as a string.
func (c *Client) String(ctx context.Context, key string, defaultValue string, evalCtx ...EvalContext) string {
	ec := mergeCtx(evalCtx)
	v, err := c.provider.Evaluate(ctx, key, defaultValue, ec)
	if err != nil {
		return defaultValue
	}
	if s, ok := v.(string); ok {
		return s
	}
	return defaultValue
}

// Int evaluates flag key as an int64.
func (c *Client) Int(ctx context.Context, key string, defaultValue int64, evalCtx ...EvalContext) int64 {
	ec := mergeCtx(evalCtx)
	v, err := c.provider.Evaluate(ctx, key, defaultValue, ec)
	if err != nil {
		return defaultValue
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return defaultValue
}

// Float evaluates flag key as a float64.
func (c *Client) Float(ctx context.Context, key string, defaultValue float64, evalCtx ...EvalContext) float64 {
	ec := mergeCtx(evalCtx)
	v, err := c.provider.Evaluate(ctx, key, defaultValue, ec)
	if err != nil {
		return defaultValue
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return defaultValue
}

func mergeCtx(ecs []EvalContext) EvalContext {
	if len(ecs) == 0 {
		return nil
	}
	return ecs[0]
}

// --- MemoryProvider ---

// MemoryProvider is an in-memory [Provider] for local development and tests.
// All flags default to their zero values when not set.
type MemoryProvider struct {
	mu    sync.RWMutex
	flags map[string]any
}

// NewMemoryProvider returns an empty MemoryProvider.
func NewMemoryProvider() *MemoryProvider {
	return &MemoryProvider{flags: map[string]any{}}
}

// Set stores key → value. Overwrites existing values.
func (p *MemoryProvider) Set(key string, value any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flags[key] = value
}

// Delete removes a flag so subsequent evaluations return the default.
func (p *MemoryProvider) Delete(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.flags, key)
}

// Metadata implements [Provider].
func (p *MemoryProvider) Metadata() ProviderMetadata {
	return ProviderMetadata{Name: "memory"}
}

// Evaluate implements [Provider].
func (p *MemoryProvider) Evaluate(_ context.Context, key string, defaultValue any, _ EvalContext) (any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.flags[key]
	if !ok {
		return defaultValue, nil
	}
	return v, nil
}

// --- NoopProvider ---

// NoopProvider always returns the default value. Useful as a safe null object
// in fx graphs where no provider is configured.
type NoopProvider struct{}

// Metadata implements [Provider].
func (NoopProvider) Metadata() ProviderMetadata { return ProviderMetadata{Name: "noop"} }

// Evaluate implements [Provider]; always returns defaultValue.
func (NoopProvider) Evaluate(_ context.Context, _ string, defaultValue any, _ EvalContext) (any, error) {
	return defaultValue, nil
}

// --- ErrUnknownFlag ---

// ErrUnknownFlag can be returned by providers that treat unknown keys as errors.
func ErrUnknownFlag(key string) error { return fmt.Errorf("flags: unknown flag %q", key) }
