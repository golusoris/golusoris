// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package numerical_test

import (
	"math"
	"testing"

	"github.com/golusoris/golusoris/science/numerical"
)

const floatTol = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= floatTol
}

func TestNewDense(t *testing.T) {
	t.Parallel()

	t.Run("positive 2x2 matrix", func(t *testing.T) {
		t.Parallel()
		data := []float64{1, 2, 3, 4}
		m := numerical.NewDense(2, 2, data)
		if m == nil {
			t.Fatal("expected non-nil matrix")
		}
		r, c := m.Dims()
		if r != 2 || c != 2 {
			t.Fatalf("dims = (%d, %d), want (2, 2)", r, c)
		}
		if got := m.At(0, 0); got != 1.0 {
			t.Fatalf("m.At(0, 0) = %f, want 1.0", got)
		}
		if got := m.At(1, 1); got != 4.0 {
			t.Fatalf("m.At(1, 1) = %f, want 4.0", got)
		}
	})

	t.Run("boundary nil data creates zero matrix", func(t *testing.T) {
		t.Parallel()
		m := numerical.NewDense(2, 3, nil)
		r, c := m.Dims()
		if r != 2 || c != 3 {
			t.Fatalf("dims = (%d, %d), want (2, 3)", r, c)
		}
		if got := m.At(0, 0); got != 0.0 {
			t.Fatalf("m.At(0, 0) = %f, want 0.0", got)
		}
	})

	t.Run("negative mismatched data size panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic on mismatched slice length")
			}
		}()
		_ = numerical.NewDense(2, 2, []float64{1, 2, 3})
	})
}

func TestMean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		vals     []float64
		want     float64
		checkNaN bool
	}{
		{
			name: "positive standard values",
			vals: []float64{1.0, 2.0, 3.0, 4.0, 5.0},
			want: 3.0,
		},
		{
			name: "positive negative numbers",
			vals: []float64{-2.0, 2.0},
			want: 0.0,
		},
		{
			name: "boundary single value",
			vals: []float64{42.0},
			want: 42.0,
		},
		{
			name:     "boundary empty slice NaN",
			vals:     []float64{},
			checkNaN: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := numerical.Mean(tc.vals)
			if tc.checkNaN {
				if !math.IsNaN(got) {
					t.Fatalf("Mean() = %f, want NaN", got)
				}
				return
			}
			if !almostEqual(got, tc.want) {
				t.Fatalf("Mean() = %f, want %f", got, tc.want)
			}
		})
	}
}

func TestStdDevAndVariance(t *testing.T) {
	t.Parallel()

	t.Run("positive variance and stddev", func(t *testing.T) {
		t.Parallel()
		vals := []float64{1.0, 2.0, 3.0}
		variance := numerical.Variance(vals)
		if !almostEqual(variance, 1.0) {
			t.Fatalf("Variance() = %f, want 1.0", variance)
		}
		stdDev := numerical.StdDev(vals)
		if !almostEqual(stdDev, 1.0) {
			t.Fatalf("StdDev() = %f, want 1.0", stdDev)
		}
	})

	t.Run("boundary identical values zero variance", func(t *testing.T) {
		t.Parallel()
		vals := []float64{5.0, 5.0, 5.0}
		variance := numerical.Variance(vals)
		if !almostEqual(variance, 0.0) {
			t.Fatalf("Variance() = %f, want 0.0", variance)
		}
		stdDev := numerical.StdDev(vals)
		if !almostEqual(stdDev, 0.0) {
			t.Fatalf("StdDev() = %f, want 0.0", stdDev)
		}
	})

	t.Run("boundary single element NaN", func(t *testing.T) {
		t.Parallel()
		vals := []float64{10.0}
		if !math.IsNaN(numerical.Variance(vals)) {
			t.Fatalf("expected NaN for Variance on single element")
		}
		if !math.IsNaN(numerical.StdDev(vals)) {
			t.Fatalf("expected NaN for StdDev on single element")
		}
	})
}

func TestDot(t *testing.T) {
	t.Parallel()

	t.Run("positive dot product", func(t *testing.T) {
		t.Parallel()
		a := []float64{1.0, 2.0, 3.0}
		b := []float64{4.0, -5.0, 6.0}
		got := numerical.Dot(a, b)
		if !almostEqual(got, 12.0) {
			t.Fatalf("Dot() = %f, want 12.0", got)
		}
	})

	t.Run("boundary single element vectors", func(t *testing.T) {
		t.Parallel()
		got := numerical.Dot([]float64{3.0}, []float64{4.0})
		if !almostEqual(got, 12.0) {
			t.Fatalf("Dot() = %f, want 12.0", got)
		}
	})

	t.Run("negative empty slice panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic on empty vectors")
			}
		}()
		_ = numerical.Dot([]float64{}, []float64{})
	})

	t.Run("negative length mismatch panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic on mismatched vector lengths")
			}
		}()
		_ = numerical.Dot([]float64{1.0}, []float64{1.0, 2.0})
	})
}

func TestNorm2(t *testing.T) {
	t.Parallel()

	t.Run("positive 3-4-5 triangle", func(t *testing.T) {
		t.Parallel()
		got := numerical.Norm2([]float64{3.0, 4.0})
		if !almostEqual(got, 5.0) {
			t.Fatalf("Norm2() = %f, want 5.0", got)
		}
	})

	t.Run("positive negative components", func(t *testing.T) {
		t.Parallel()
		got := numerical.Norm2([]float64{-3.0, -4.0})
		if !almostEqual(got, 5.0) {
			t.Fatalf("Norm2() = %f, want 5.0", got)
		}
	})

	t.Run("boundary zero vector", func(t *testing.T) {
		t.Parallel()
		got := numerical.Norm2([]float64{0.0, 0.0, 0.0})
		if !almostEqual(got, 0.0) {
			t.Fatalf("Norm2() = %f, want 0.0", got)
		}
	})

	t.Run("boundary single element", func(t *testing.T) {
		t.Parallel()
		got := numerical.Norm2([]float64{-7.0})
		if !almostEqual(got, 7.0) {
			t.Fatalf("Norm2() = %f, want 7.0", got)
		}
	})

	t.Run("negative empty slice panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic on empty vector")
			}
		}()
		_ = numerical.Norm2([]float64{})
	})
}
