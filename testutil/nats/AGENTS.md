<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/nats/

Spins real NATS via testcontainers-go; JetStream enabled; URL returned.

NATS + Ryuk references: immutable `internal/testimages` authority. Renovate owns
tag + digest updates. No local mutable image strings.

## Usage

```go
import natstestutil "github.com/golusoris/golusoris/testutil/nats"

func TestFoo(t *testing.T) {
    url := natstestutil.Start(t) // "nats://127.0.0.1:<port>"
    // wire pubsub/nats.Module with this URL via config
}
```

## Don't

- Don't share container across parallel tests — each call creates its own
 container; isolation is point.
- Don't use this package when unit test suffices. Spin containers only when
 real NATS behaviour (pub/sub delivery, JetStream persistence) must be
 verified.
