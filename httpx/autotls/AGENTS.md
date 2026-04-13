# Agent guide — httpx/autotls

Pluggable auto-TLS. Two implementations:

| Sub-package | Backend | When to pick |
|---|---|---|
| `httpx/autotls/autocert` | x/crypto/acme/autocert | Lean, stdlib-ish, single-replica. |
| `httpx/autotls/certmagic` | caddyserver/certmagic | On-demand issuance, distributed storage, DNS-01 providers. |

Apps pick ONE sub-module and add it to the fx graph. The sub-module provides a `*tls.Config` that `httpx/server` picks up via optional dependency — plaintext otherwise.

## Don't

- Don't wire both autocert + certmagic. They conflict — the fx graph will get two providers for `*tls.Config`.
- Don't use autotls when terminating TLS at a load balancer (k8s ingress, AWS ALB). The LB already handles issuance + rotation.
