package prop_test

import (
	"sort"
	"testing"

	"github.com/leanovate/gopter/gen"
	goprop "github.com/leanovate/gopter/prop"

	"github.com/golusoris/golusoris/testutil/prop"
)

func TestNew_deterministic(t *testing.T) {
	t.Parallel()
	// Two Properties built from the same t should share the same seed.
	// We can't directly compare RNG state, but we can verify Run doesn't panic.
	props := prop.New(t)
	props.Property("trivially true", goprop.ForAll(
		func(n int) bool { _ = n; return true },
		gen.Int(),
	))
	props.TestingRun(t)
}

func TestRun_sortIsIdempotent(t *testing.T) {
	t.Parallel()
	prop.Run(t, func(props *prop.Properties) {
		props.Property("sort twice = sort once", goprop.ForAll(
			func(xs []int) bool {
				a := make([]int, len(xs))
				copy(a, xs)
				sort.Ints(a)
				b := make([]int, len(a))
				copy(b, a)
				sort.Ints(b)
				for i := range a {
					if a[i] != b[i] {
						return false
					}
				}
				return true
			},
			gen.SliceOf(gen.Int()),
		))
	})
}

func TestRun_roundTrip(t *testing.T) {
	t.Parallel()
	prop.Run(t, func(props *prop.Properties) {
		props.Property("len round-trip", goprop.ForAll(
			func(s string) bool { return len([]byte(s)) >= len(s) },
			gen.AnyString(),
		))
	})
}
