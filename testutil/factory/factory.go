// Package factory provides test data factories backed by brianvoe/gofakeit.
//
// It re-exports [gofakeit.Faker] and adds seed helpers so tests are
// deterministic by default.
//
// Usage:
//
//	f := factory.New(t)          // seeded from t.Name() for determinism
//	email := f.Email()
//	name  := f.Name()
//	uuid  := f.UUID()
//
//	f2 := factory.Random()       // random seed (non-deterministic)
package factory

import (
	"hash/fnv"
	"testing"

	"github.com/brianvoe/gofakeit/v6"
)

// Faker is an alias for gofakeit.Faker.
type Faker = gofakeit.Faker

// New returns a deterministic Faker seeded from the test name.
// The same test always generates the same sequence of fake values.
func New(t *testing.T) *Faker {
	t.Helper()
	h := fnv.New64a()
	_, _ = h.Write([]byte(t.Name()))
	return gofakeit.New(int64(h.Sum64())) //nolint:gosec // seed is not security-critical
}

// Random returns a Faker with a random seed.
// Use when determinism is not required.
func Random() *Faker { return gofakeit.New(0) }

