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
