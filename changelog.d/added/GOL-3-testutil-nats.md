- `testutil/nats`: new testcontainers helper that spins a real NATS server
  (JetStream enabled) and returns its URL for integration tests.
- `pubsub/nats`: added testcontainers integration tests covering connect/ping,
  core publish/subscribe delivery, and JetStream availability via the fx
  lifecycle (`TestIntegration_ConnectAndPing`, `TestIntegration_PublishSubscribe`,
  `TestIntegration_JetStreamAvailable`).
