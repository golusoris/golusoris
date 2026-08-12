# Agent guide — testutil/nats/

Spins up a real NATS container (testcontainers-go generic API, `nats:2-alpine`,
JetStream enabled) and returns its URL for integration tests.

## Usage

```go
import natstestutil "github.com/golusoris/golusoris/testutil/nats"

func TestFoo(t *testing.T) {
    url := natstestutil.Start(t) // "nats://127.0.0.1:<port>"
    // wire pubsub/nats.Module with this URL via config
}
```

## Don't

- Don't share the container across parallel tests — each call creates its own
  container; isolation is the point.
- Don't use this package when a unit test suffices. Spin containers only when
  real NATS behaviour (pub/sub delivery, JetStream persistence) must be
  verified.
