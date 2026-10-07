// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package bootstrap is the lean entry point for services: the foundational
// fx groupings, without linking the rest of the framework.
//
// Importing the root golusoris package links every subsystem its groupings
// reference. A service that needs configuration, logging and an HTTP server
// imports this package instead and adds sub-package modules as it needs them:
//
//	fx.New(
//	    bootstrap.Core,
//	    bootstrap.HTTP,
//	    otel.Module, // github.com/golusoris/golusoris/otel
//	).Run()
//
// The root package's Core and HTTP are these same values.
package bootstrap

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/crypto"
	"github.com/golusoris/golusoris/core/id"
	"github.com/golusoris/golusoris/core/log"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/httpx/router"
	"github.com/golusoris/golusoris/httpx/server"
)

// Core bundles the foundational modules every app needs:
// config, log, clock, id, validate, crypto.
//
// errors/ and i18n/ are intentionally not in fx: errors is a pure package and
// i18n is opt-in.
var Core = fx.Module(
	"golusoris.core",
	config.Module,
	log.Module,
	clock.Module,
	id.Module,
	validate.Module,
	crypto.Module,
)

// HTTP bundles the base HTTP stack: chi router + *http.Server with
// slow-loris guards, body limits, and graceful shutdown. Apps add
// middleware via fx.Invoke against the provided chi.Router.
//
// Individual httpx/middleware functions are not in fx (they're plain
// net/http middleware); apps compose the stack they want and register it
// via router.Use.
var HTTP = fx.Module(
	"golusoris.http",
	router.Module,
	server.Module,
)
