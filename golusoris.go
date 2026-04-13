// Package golusoris is the top-level entry: it re-exports composable [fx.Module]
// groupings so apps can compose only what they need.
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.DB,
//	    golusoris.HTTP,
//	).Run()
//
// Subpackages provide the actual implementations. The groupings here just
// bundle commonly-used sets so app wiring stays terse.
package golusoris

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/crypto"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/httpx/router"
	"github.com/golusoris/golusoris/httpx/server"
	"github.com/golusoris/golusoris/id"
	"github.com/golusoris/golusoris/log"
	"github.com/golusoris/golusoris/validate"
)

// Core bundles the foundational modules every app needs:
// config, log, clock, id, validate, crypto.
//
// errors/ and i18n/ are intentionally not in fx — errors is a pure package and
// i18n is opt-in via [I18n].
var Core = fx.Module("golusoris.core",
	config.Module,
	log.Module,
	clock.Module,
	id.Module,
	validate.Module,
	crypto.Module,
)

// DB bundles the database modules: pgx pool + golang-migrate runner.
// Requires [Core] in the same fx graph for config, log, and clock.
//
// db/sqlc helpers are stateless (no fx wiring), so they're available via
// direct import without inclusion here.
var DB = fx.Module("golusoris.db",
	dbpgx.Module,
	dbmigrate.Module,
)

// HTTP bundles the base HTTP stack: chi router + *http.Server with
// slow-loris guards, body limits, and graceful shutdown. Apps add
// middleware via fx.Invoke against the provided chi.Router.
//
// Individual httpx/middleware functions are not in fx (they're plain
// net/http middleware); apps compose the stack they want and register it
// via router.Use.
var HTTP = fx.Module("golusoris.http",
	router.Module,
	server.Module,
)
