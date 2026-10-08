<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/pact/

Helpers for Pact consumer-driven contract testing over `pact-foundation/pact-go/v2`:
consumer-side HTTP mock and provider-side verifier. Stateless test utility —
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

`AddInteraction` returns raw pact-go builder, so full V2 DSL is reachable.
All helpers `t.Fatalf` on error.

## Notes

- **Own go.mod sub-module** (`github.com/golusoris/golusoris/testutil/pact`):
  pact-go v2 uses native Pact FFI, kept out of production builds. Import
  sub-module path directly. CI installs reviewed FFI version; verifies
  checked-in release-asset SHA-256 before build, vet, and test. Update
  `scripts/ci/pact_ffi.py` alongside pact-go pin.
- `ProviderOptions`: supply either `PactURLs` (local files / HTTP) **or**
 `BrokerURL` + `ConsumerVersionSelectors` — broker takes precedence over URLs.
