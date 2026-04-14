// Package numerical provides gonum-based linear algebra, statistics, and
// optimization helpers.
//
// This is a separate go.mod sub-module because gonum pulls large test-data
// assets that would bloat the main framework's vendor tree.
// Import directly: github.com/golusoris/golusoris/science/numerical
//
// Activate by including this sub-module in your app's go.work or go.mod.
package numerical

import (
	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat"
)

// NewDense wraps mat.NewDense for construction without a direct gonum import.
func NewDense(r, c int, data []float64) *mat.Dense {
	return mat.NewDense(r, c, data)
}

// Mean returns the arithmetic mean of vals.
func Mean(vals []float64) float64 {
	return stat.Mean(vals, nil)
}

// StdDev returns the sample standard deviation of vals.
func StdDev(vals []float64) float64 {
	return stat.StdDev(vals, nil)
}

// Variance returns the sample variance of vals.
func Variance(vals []float64) float64 {
	return stat.Variance(vals, nil)
}

// Dot returns the dot product of two equal-length slices.
func Dot(a, b []float64) float64 {
	v1 := mat.NewVecDense(len(a), a)
	v2 := mat.NewVecDense(len(b), b)
	return mat.Dot(v1, v2)
}

// Norm2 returns the L2 norm of v.
func Norm2(v []float64) float64 {
	vec := mat.NewVecDense(len(v), v)
	return mat.Norm(vec, 2)
}
