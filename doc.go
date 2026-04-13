// Package golusoris is a composable Go framework built around [go.uber.org/fx].
//
// It bundles best-in-class libraries behind opt-in fx modules so apps share a
// single source of truth for cross-cutting concerns: configuration, logging,
// errors, database, HTTP, observability, jobs, cache, auth, k8s runtime,
// notifications, files, AI, and more.
//
// # Composing an app
//
// Apps import this package and pick the modules they need:
//
//	package main
//
//	import (
//	    "go.uber.org/fx"
//	    "github.com/golusoris/golusoris"
//	)
//
//	func main() {
//	    fx.New(
//	        golusoris.Core, // config + log + clock + id + validate + crypto
//	        // golusoris.DB,         // pgx pool + migrations + sqlc helpers
//	        // golusoris.HTTP,       // server + middleware + Scalar API docs
//	        // golusoris.OTel,       // tracer + meter + logs + OTLP exporter
//	        // golusoris.Jobs,       // river queue
//	        // ... pick what you need
//	    ).Run()
//	}
//
// # Subpackages
//
// Each capability lives in its own subpackage with its own [fx.Module]:
//
//   - [config] — koanf v2 with file-watch + SIGHUP reload
//   - [log] — slog with tint(dev) / json(prod) factory
//   - [errors] — typed coded errors with HTTP status mapping
//   - [crypto] — argon2id + AES-GCM + secure random
//   - [clock] — mockable wall clock for testable time-sensitive code
//   - [id] — UUIDv7 + KSUID generators
//   - [validate] — go-playground/validator wrapper with golusoris error mapping
//   - [i18n] — go-i18n bundle with HTTP locale negotiation
//
// More modules are documented in their own packages.
//
// # Conventions
//
// All time uses [clock.Clock] (never [time.Now] outside the clock package).
// All errors flow through the [errors] package or wrap [github.com/go-faster/errors]
// directly. All logs use the [*log/slog.Logger] from [log]. No init() side
// effects — wiring happens through [go.uber.org/fx] lifecycle hooks.
//
// # Versioning
//
// Trunk-based development with [release-please] generating tagged releases
// from conventional commits. Breaking changes require a "Migration:" footer
// in the commit body, auto-stitched into per-version migration guides under
// docs/migrations/. CI gates breaking changes via apidiff.
//
// [release-please]: https://github.com/googleapis/release-please
package golusoris
