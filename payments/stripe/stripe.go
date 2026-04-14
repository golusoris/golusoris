// Package stripe provides a thin fx-compatible wrapper around stripe-go/v82.
// It exposes a [Client] covering the most common SaaS payment flows:
// payment intents, checkout sessions, and customer portal.
// The inbound webhook signature middleware lives in [webhooks/in.Stripe].
//
// Picks: stripe/stripe-go v82 (official SDK, v82 new-style stripe.Client API).
//
// Usage:
//
//	// In fx:
//	fx.New(stripe.Module)
//
//	// Inject:
//	fx.Invoke(func(c *stripe.Client) {
//	    url, err := c.NewCheckoutSession(ctx, stripe.CheckoutParams{...})
//	})
package stripe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	sdk "github.com/stripe/stripe-go/v82"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options configures the Stripe client.
type Options struct {
	// SecretKey is the Stripe secret API key (sk_live_… or sk_test_…).
	SecretKey string `koanf:"secret_key"`
	// WebhookSecret is used by webhooks/in.Stripe middleware; stored here
	// for centralised config.
	WebhookSecret string `koanf:"webhook_secret"`
}

// CheckoutParams describes a Stripe Checkout session.
type CheckoutParams struct {
	CustomerID        string
	LineItems         map[string]int64 // priceID → quantity
	SuccessURL        string
	CancelURL         string
	Mode              string // "payment" | "subscription" | "setup"
	ClientReferenceID string
	Metadata          map[string]string
}

// PortalParams describes a Stripe Customer Portal session.
type PortalParams struct {
	CustomerID string
	ReturnURL  string
}

// Client wraps the Stripe API for common SaaS flows.
type Client struct {
	sc     *sdk.Client
	logger *slog.Logger
}

// New returns a Client using the provided secret key.
func New(opts Options, logger *slog.Logger) (*Client, error) {
	if opts.SecretKey == "" {
		return nil, errors.New("stripe: secret key is required")
	}
	sc := sdk.NewClient(opts.SecretKey)
	return &Client{sc: sc, logger: logger}, nil
}

// NewCheckoutSession creates a Stripe Checkout session and returns its URL.
func (c *Client) NewCheckoutSession(ctx context.Context, p CheckoutParams) (string, error) {
	if p.Mode == "" {
		p.Mode = "payment"
	}

	items := make([]*sdk.CheckoutSessionCreateLineItemParams, 0, len(p.LineItems))
	for priceID, qty := range p.LineItems {
		items = append(items, &sdk.CheckoutSessionCreateLineItemParams{
			Price:    sdk.String(priceID),
			Quantity: sdk.Int64(qty),
		})
	}

	params := &sdk.CheckoutSessionCreateParams{
		Mode:       sdk.String(p.Mode),
		LineItems:  items,
		SuccessURL: sdk.String(p.SuccessURL),
		CancelURL:  sdk.String(p.CancelURL),
	}
	if p.CustomerID != "" {
		params.Customer = sdk.String(p.CustomerID)
	}
	if p.ClientReferenceID != "" {
		params.ClientReferenceID = sdk.String(p.ClientReferenceID)
	}
	for k, v := range p.Metadata {
		if params.Metadata == nil {
			params.Metadata = map[string]string{}
		}
		params.Metadata[k] = v
	}

	sess, err := c.sc.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("stripe: checkout session: %w", err)
	}
	return sess.URL, nil
}

// NewPortalSession creates a Stripe Customer Portal session.
func (c *Client) NewPortalSession(ctx context.Context, p PortalParams) (string, error) {
	sess, err := c.sc.V1BillingPortalSessions.Create(ctx, &sdk.BillingPortalSessionCreateParams{
		Customer:  sdk.String(p.CustomerID),
		ReturnURL: sdk.String(p.ReturnURL),
	})
	if err != nil {
		return "", fmt.Errorf("stripe: portal session: %w", err)
	}
	return sess.URL, nil
}

// CreatePaymentIntent creates a PaymentIntent for direct charge flows.
func (c *Client) CreatePaymentIntent(ctx context.Context, amount int64, currency, customerID string) (*sdk.PaymentIntent, error) {
	params := &sdk.PaymentIntentCreateParams{
		Amount:   sdk.Int64(amount),
		Currency: sdk.String(currency),
	}
	if customerID != "" {
		params.Customer = sdk.String(customerID)
	}
	pi, err := c.sc.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("stripe: payment intent: %w", err)
	}
	return pi, nil
}

// --- fx wiring ---

// Module provides *stripe.Client to the fx graph.
// Requires config key prefix "payments.stripe".
var Module = fx.Module("golusoris.payments.stripe",
	fx.Provide(newFromConfig),
)

func newFromConfig(cfg *config.Config, logger *slog.Logger) (*Client, error) {
	var opts Options
	if err := cfg.Unmarshal("payments.stripe", &opts); err != nil {
		return nil, fmt.Errorf("stripe: config: %w", err)
	}
	return New(opts, logger)
}
