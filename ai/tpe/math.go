// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from VMAFx/vmafx pkg/prefilter/tpe.go at 73c76b24a.

package tpe

import (
	"math"
)

// logSumExp computes log(sum(exp(v))) without overflow.
func logSumExp(values []float64) float64 {
	if len(values) == 0 {
		return math.Inf(-1)
	}
	maxV := math.Inf(-1)
	for _, v := range values {
		if v > maxV {
			maxV = v
		}
	}
	if math.IsInf(maxV, -1) {
		return maxV
	}
	sum := 0.0
	for _, v := range values {
		sum += math.Exp(v - maxV)
	}
	return maxV + math.Log(sum)
}

// normalCDF is the standard normal cumulative distribution function.
func normalCDF(z float64) float64 {
	return float64(0.5 * math.Erfc(-z/math.Sqrt2))
}

// normalPPF is the standard normal inverse CDF (probit), via the Acklam
// rational approximation refined by one Halley step against erfc.
//
// The upper half is evaluated by the symmetry PPF(p) = -PPF(1-p) rather than
// directly: the Halley step's residual 0.5*erfc(-x/sqrt2) - p cancels
// catastrophically as p approaches 1 (both terms approach 1), costing several
// digits in the far upper tail. Reflecting keeps every evaluation in the
// well-conditioned lower half, and makes the function exactly antisymmetric,
// which the truncated-normal sampler relies on for symmetric components.
func normalPPF(p float64) float64 {
	switch {
	case p <= 0.0:
		return math.Inf(-1)
	case p >= 1.0:
		return math.Inf(1)
	case p > 0.5:
		// 1-p lies strictly below 0.5 for every p in (0.5, 1), so the
		// reflection always lands in the lower half and needs the core
		// directly rather than a self-call (HISS-01).
		return -normalPPFLowerHalf(1.0 - p)
	}
	return normalPPFLowerHalf(p)
}

// normalPPFLowerHalf evaluates the probit for p <= 0.5 (and for NaN, which
// falls through every guard in normalPPF exactly as it did before).
func normalPPFLowerHalf(p float64) float64 {
	// Acklam's coefficients.
	a := [6]float64{
		-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00,
	}
	b := [5]float64{
		-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01,
	}
	c := [6]float64{
		-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00,
	}
	d := [4]float64{
		7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00,
	}
	const pLow = 0.02425
	pHigh := 1.0 - pLow

	var x float64
	switch {
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		x = horner(q, c[:]) /
			horner(q, append(d[:], 1))
	case p <= pHigh:
		q := p - 0.5
		r := q * q
		x = horner(r, a[:]) * q /
			horner(r, append(b[:], 1))
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		x = -horner(q, c[:]) /
			horner(q, append(d[:], 1))
	}

	// One Halley refinement step against the true CDF.
	e := float64(0.5*math.Erfc(-x/math.Sqrt2)) - p
	u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
	return x - u/(1+float64(x*u/2))
}

// WHY: explicit float64 rounding stops arm64 from fusing x*y+z (Go spec allows it) so results match amd64.
func horner(x float64, c []float64) float64 {
	acc := c[0]
	for _, ci := range c[1:] {
		acc = float64(acc*x) + ci
	}
	return acc
}
