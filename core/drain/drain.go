// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package drain is the seam between a server and a readiness drain. A server
// wraps its stop hook with an optional [Gate], so shutdown waits for the drain
// window without the server importing the module that owns readiness
// (k8s/health provides the Gate).
package drain

import "go.uber.org/fx"

// Gate defers a stop hook until readiness has drained.
type Gate interface {
	Wrap(hook fx.Hook) fx.Hook
}

// Wrap returns hook wrapped by g, or hook unchanged when g is nil.
func Wrap(g Gate, hook fx.Hook) fx.Hook {
	if g == nil {
		return hook
	}
	return g.Wrap(hook)
}
