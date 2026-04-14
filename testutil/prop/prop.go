// Package prop provides property-based testing helpers backed by
// leanovate/gopter.
//
// It adds a deterministic-seed [New] constructor (seeded from the test name)
// so property tests produce the same sequence on every run, and a [Run]
// helper for single-group property suites.
//
// The full gopter API (generators, combinators, reporting) is available via
// the re-exported sub-packages; apps import them directly when needed.
//
// Usage:
//
//	import (
//	    "github.com/golusoris/golusoris/testutil/prop"
//	    "github.com/leanovate/gopter/gen"
//	    "github.com/leanovate/gopter/prop"
//	)
//
//	func TestEncodeDecode(t *testing.T) {
//	    props := prop.New(t)
//	    props.Property("round-trip", goprop.ForAll(
//	        func(s string) bool {
//	            got, _ := decode(encode(s))
//	            return got == s
//	        },
//	        gen.AnyString(),
//	    ))
//	    props.TestingRun(t)
//	}
package prop

import (
	"hash/fnv"
	"math/rand"
	"testing"

	"github.com/leanovate/gopter"
)

// Properties is an alias for [gopter.Properties].
type Properties = gopter.Properties

// New returns a [gopter.Properties] with test parameters seeded
// deterministically from t.Name(). The same test always exercises the same
// input sequence, which makes failures reproducible without -count=1.
//
// Call [Properties.TestingRun] when all properties have been added.
func New(t *testing.T) *Properties {
	t.Helper()
	h := fnv.New64a()
	_, _ = h.Write([]byte(t.Name()))
	params := gopter.DefaultTestParameters()
	params.Rng = rand.New(rand.NewSource(int64(h.Sum64()))) //nolint:gosec // G404: test RNG not crypto; G115: conversion is safe, Sum64 fits int64 range in practice
	return gopter.NewProperties(params)
}

// Run is a convenience wrapper: it creates a [Properties] seeded from t.Name(),
// calls setup to register property assertions, then immediately runs them.
//
//	prop.Run(t, func(props *prop.Properties) {
//	    props.Property("sort is idempotent", prop.ForAll(
//	        func(xs []int) bool { ... },
//	        gen.SliceOf(gen.Int()),
//	    ))
//	})
func Run(t *testing.T, setup func(*Properties)) {
	t.Helper()
	props := New(t)
	setup(props)
	props.TestingRun(t)
}
