// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"
)

// TracingOptions toggles River's OpenTelemetry middleware (otelriver): insert
// and work spans plus river.* metrics on the global OTel providers, which the
// otel/ module installs.
type TracingOptions struct {
	// Enabled adds the middleware to the client.
	Enabled bool `koanf:"enabled"`
	// Propagate carries W3C trace context in job metadata so work spans link
	// back to the inserting span (default true via DefaultOptions).
	Propagate bool `koanf:"propagate"`
}

func tracingPlugins(o TracingOptions) []rivertype.Plugin {
	if !o.Enabled {
		return nil
	}
	return []rivertype.Plugin{otelriver.NewMiddleware(&otelriver.MiddlewareConfig{
		DurationUnit:           "s", // NewMiddleware panics on any other unit than "s"/"ms"
		EnableTracePropagation: o.Propagate,
	})}
}
