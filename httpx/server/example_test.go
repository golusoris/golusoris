// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package server_test

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris"
	"github.com/golusoris/golusoris/httpx/router"
	"github.com/golusoris/golusoris/httpx/server"
)

// ExampleModule wires the server with a router so routes can be registered
// via fx.Invoke.
func ExampleModule() {
	app := fx.New(
		fx.NopLogger,
		golusoris.Core,
		router.Module,
		server.Module,
	)
	_ = app // app.Run() in production
	// Output:
}
