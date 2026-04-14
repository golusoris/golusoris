// Package pact provides helpers for Pact consumer-driven contract testing.
//
// This is a separate go.mod sub-module because pact-go v2 embeds a Ruby
// standalone binary (~40 MB) that is irrelevant in production builds.
// Import directly: github.com/golusoris/golusoris/testutil/pact
//
// # Consumer test (HTTP)
//
//	func TestMyConsumer(t *testing.T) {
//	    pact := pact.NewHTTPPact(t, "MyConsumer", "MyProvider")
//	    pact.AddInteraction().
//	        UponReceiving("a GET /users request").
//	        WithRequest(consumer.Request{Method: "GET", Path: "/users"}).
//	        WillRespondWith(consumer.Response{Status: 200})
//	    pact.ExecuteTest(t, func(config consumer.MockServerConfig) error {
//	        // call your client against config.Host:config.Port
//	        return nil
//	    })
//	}
//
// # Provider verification
//
//	func TestMyProvider(t *testing.T) {
//	    pact.VerifyProvider(t, pact.ProviderOptions{
//	        Provider:        "MyProvider",
//	        ProviderBaseURL: "http://localhost:8080",
//	        PactURLs:        []string{"./pacts/myconsumer-myprovider.json"},
//	    })
//	}
package pact

import (
	"testing"

	"github.com/pact-foundation/pact-go/v2/consumer"
	"github.com/pact-foundation/pact-go/v2/provider"
)

// HTTPPact wraps consumer.NewV2Pact for HTTP contract testing.
type HTTPPact struct {
	p *consumer.V2HTTPMockProvider
}

// NewHTTPPact creates a new Pact consumer mock for HTTP interactions.
func NewHTTPPact(t *testing.T, consumerName, providerName string) *HTTPPact {
	t.Helper()
	p, err := consumer.NewV2Pact(consumer.MockHTTPProviderConfig{
		Consumer: consumerName,
		Provider: providerName,
	})
	if err != nil {
		t.Fatalf("pact: new pact: %v", err)
	}
	return &HTTPPact{p: p}
}

// AddInteraction registers an expected HTTP interaction.
func (h *HTTPPact) AddInteraction() *consumer.Interaction {
	return h.p.AddInteraction()
}

// ExecuteTest runs the consumer test against the Pact mock server.
func (h *HTTPPact) ExecuteTest(t *testing.T, fn func(config consumer.MockServerConfig) error) {
	t.Helper()
	if err := h.p.ExecuteTest(t, fn); err != nil {
		t.Fatalf("pact: execute test: %v", err)
	}
}

// ProviderOptions configures a provider verification run.
type ProviderOptions struct {
	Provider        string
	ProviderBaseURL string
	// PactURLs are local file paths or HTTP URLs to .json pact files.
	PactURLs []string
	// BrokerURL is the Pact Broker URL (optional; used instead of PactURLs).
	BrokerURL string
	// ConsumerVersionSelectors selects consumer versions from the broker.
	ConsumerVersionSelectors []provider.ConsumerVersionSelector
}

// VerifyProvider runs provider-side pact verification.
func VerifyProvider(t *testing.T, opts ProviderOptions) {
	t.Helper()
	verifier := provider.NewVerifier()
	err := verifier.VerifyProvider(t, provider.VerifyRequest{
		Provider:                 opts.Provider,
		ProviderBaseURL:          opts.ProviderBaseURL,
		PactURLs:                 opts.PactURLs,
		BrokerURL:                opts.BrokerURL,
		ConsumerVersionSelectors: opts.ConsumerVersionSelectors,
	})
	if err != nil {
		t.Fatalf("pact: provider verification: %v", err)
	}
}
