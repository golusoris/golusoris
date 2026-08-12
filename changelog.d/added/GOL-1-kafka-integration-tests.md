Add testcontainers integration coverage for `pubsub/kafka` via `testutil/kafka`
(Redpanda-backed); ships `integration_test.go`, `example_test.go`, and
`kafka.ClientFromKgo` constructor for test-only wiring without the fx stack.
