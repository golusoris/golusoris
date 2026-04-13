# Agent guide — payments/stripe/

Thin fx-compatible wrapper around stripe-go/v82 (new `stripe.Client` API).
Covers Checkout sessions, Customer Portal, and Payment Intents.
Webhook signature verification lives in `webhooks/in.Stripe`.

## fx module

```go
fx.New(stripe.Module) // requires "payments.stripe.secret_key" in config
```

Config keys (koanf prefix `payments.stripe`):

| Key | Purpose |
|---|---|
| `secret_key` | Stripe secret API key (`sk_live_…` or `sk_test_…`) |
| `webhook_secret` | Webhook endpoint secret (used by `webhooks/in.Stripe`) |

## Client methods

```go
// Checkout session → redirect URL:
url, err := client.NewCheckoutSession(ctx, stripe.CheckoutParams{
    CustomerID: "cus_xxx",
    LineItems:  map[string]int64{"price_xxx": 1},
    SuccessURL: "https://example.com/thanks",
    CancelURL:  "https://example.com/cancel",
    Mode:       "payment", // or "subscription"
})

// Customer portal → redirect URL:
url, err = client.NewPortalSession(ctx, stripe.PortalParams{
    CustomerID: "cus_xxx",
    ReturnURL:  "https://example.com/billing",
})

// Payment intent (for Elements / manual flows):
pi, err := client.CreatePaymentIntent(ctx, 999, "usd", "cus_xxx")
```

## Deferred sub-packages

- `payments/subs/` — subscription state machine (plans, seats, trial, proration, dunning)
- `payments/meter/` — usage metering + exports
- `payments/invoice/` — PDF invoicing (depends on `pdf/`)

## Don't

- Don't log `pi.ClientSecret` — it grants access to the payment method.
- Don't hardcode the secret key — always use env vars / `secrets/`.
- Don't call Stripe APIs in hot paths without timeouts.
