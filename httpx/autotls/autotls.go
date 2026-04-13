// Package autotls defines a pluggable TLS interface for auto-issued
// certificates. Two implementations ship in sub-packages:
//
//   - [httpx/autotls/autocert] — x/crypto/acme/autocert. Lean, stdlib-ish.
//   - [httpx/autotls/certmagic] — caddyserver/certmagic. Richer (on-demand
//     issuance, distributed storage, multi-provider DNS-01).
//
// Apps pick ONE sub-package and import its Module. The provider supplies a
// *tls.Config that [httpx/server] picks up via the [Provider] fx group.
//
// If no autotls provider is wired, the server listens plaintext (current
// default). The server enables TLS iff a *tls.Config is supplied.
package autotls

import "crypto/tls"

// Provider is the common interface both auto-TLS backends implement.
// Downstream code injects *tls.Config directly — this interface exists so
// each backend's fx.Module has a uniform surface to document against.
type Provider interface {
	TLSConfig() *tls.Config
}
