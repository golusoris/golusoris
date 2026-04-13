# Agent guide — k8s/leader

Single-leader election via the k8s Lease API (client-go).

## Conventions

- One Lease per "thing being elected" — `leader.name` is the Lease name,
  unique per task. Don't share Leases across unrelated leaders in the same
  app; create separate Module instances with distinct names.
- Default timing (15s lease / 10s renew / 2s retry) matches client-go's
  controller defaults — battle-tested. Lower values for fast failover at
  the cost of more API server traffic.
- `leader.identity` defaults to `POD_NAME` env (downward API) → hostname →
  "unknown". Always wire downward API in production so leader logs cite
  the pod name.
- Callbacks:
  * `OnStartedLeading(ctx)` runs in a fresh goroutine when this pod wins.
    The supplied ctx is canceled when the lease is lost — your code MUST
    return promptly on cancellation or risk concurrent leaders.
  * `OnStoppedLeading()` runs after that ctx is fully drained.
  * `OnNewLeader(identity)` fires whenever any pod (including this one)
    becomes leader — useful for metrics + logs.

## Don't

- Don't run business logic that must be serialized against external state
  (e.g. cron, outbox dispatcher) on every replica. Wrap it in a leader
  callback.
- Don't set `lease.duration < 2 * lease.renew` — client-go's elector
  refuses configurations where renewal can't keep up with expiry.
