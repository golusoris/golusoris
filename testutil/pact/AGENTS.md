# Agent guide — testutil/pact/

Helpers for Pact consumer-driven contract testing over `pact-foundation/pact-go/v2`:
a consumer-side HTTP mock and a provider-side verifier. Stateless test utility —
**no fx wiring**.

## API

```go
p := pact.NewHTTPPact(t, "MyConsumer", "MyProvider")  // wraps consumer.NewV2Pact
p.AddInteraction().                                   // *consumer.V2UnconfiguredInteraction
    UponReceiving("...").WithRequest(...).WillRespondWith(...)
p.ExecuteTest(t, func(cfg consumer.MockServerConfig) error { /* call client */ return nil })

pact.VerifyProvider(t, pact.ProviderOptions{
    Provider:        "MyProvider",
    ProviderBaseURL: "http://localhost:8080",
    PactURLs:        []string{"./pacts/myconsumer-myprovider.json"}, // or BrokerURL + selectors
})
```

`AddInteraction` returns the raw pact-go builder, so the full V2 DSL is reachable.
All helpers `t.Fatalf` on error.

## Notes

- **Own go.mod sub-module** (`github.com/golusoris/golusoris/testutil/pact`):
  pact-go v2 embeds a ~40 MB Ruby standalone binary, kept out of production
  builds. Import the sub-module path directly.
- `ProviderOptions`: supply either `PactURLs` (local files / HTTP) **or**
  `BrokerURL` + `ConsumerVersionSelectors` — broker takes precedence over URLs.
