// Package fxtest provides helpers for testing fx applications.
//
// It wraps go.uber.org/fx/fxtest with convenience functions that integrate
// with Go's *testing.T lifecycle.
//
// Usage:
//
//	func TestMyService(t *testing.T) {
//	    var svc *MyService
//	    fxtest.New(t,
//	        myservice.Module,
//	        fx.Populate(&svc),
//	    )
//	    // svc is started and will be stopped when t completes
//	    result := svc.DoThing(ctx)
//	    ...
//	}
package fxtest

import (
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// New creates a started fx app for the duration of the test.
// The app is stopped automatically via t.Cleanup.
// If the app fails to start, the test is marked fatal.
func New(t *testing.T, opts ...fx.Option) *fxtest.App {
	t.Helper()
	app := fxtest.New(t, opts...)
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })
	return app
}

// Populate is a convenience wrapper for fx.Populate that extracts a value
// from the fx container into target. Use it alongside New:
//
//	var svc *MyService
//	fxtest.New(t, myservice.Module, fxtest.Populate(&svc))
func Populate(targets ...any) fx.Option {
	return fx.Populate(targets...)
}
