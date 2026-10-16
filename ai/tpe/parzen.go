// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from VMAFx/vmafx pkg/prefilter/tpe.go at 73c76b24a.

package tpe

import (
	"math"
	"math/rand"
	"sort"
)

// parzen is a mixture of truncated Gaussians over [lo, hi].
type parzen struct {
	mus     []float64
	sigmas  []float64
	weights []float64
	lo      float64
	hi      float64
}

// newParzen fits the estimator to observations on dim.
//
// Bandwidths follow the Bergstra heuristic: each component's sigma is the
// larger gap to its sorted neighbours, floored so a dense cluster cannot
// collapse to a spike and capped at the full range. The prior component is
// centred on the midpoint with sigma equal to the whole range, which keeps
// the estimator from ever assigning zero density inside the box.
func newParzen(values []float64, dim Dimension, priorWeight float64) *parzen {
	span := dim.Hi - dim.Lo
	if span <= 0 {
		span = 1.0
	}
	p := &parzen{lo: dim.Lo, hi: dim.Hi}

	// Prior component first, so an empty observation set still yields a
	// proper distribution.
	p.mus = append(p.mus, (dim.Lo+dim.Hi)/2.0)
	p.sigmas = append(p.sigmas, span)
	p.weights = append(p.weights, priorWeight)

	if len(values) > 0 {
		sorted := make([]float64, len(values))
		copy(sorted, values)
		sort.Float64s(sorted)

		minSigma := span / math.Min(100.0, float64(len(sorted)+1))
		for i, v := range sorted {
			left, right := span, span
			if i > 0 {
				left = v - sorted[i-1]
			}
			if i < len(sorted)-1 {
				right = sorted[i+1] - v
			}
			sigma := math.Max(left, right)
			sigma = math.Max(sigma, minSigma)
			sigma = math.Min(sigma, span)
			p.mus = append(p.mus, v)
			p.sigmas = append(p.sigmas, sigma)
			p.weights = append(p.weights, 1.0)
		}
	}

	total := 0.0
	for _, w := range p.weights {
		total += w
	}
	for i := range p.weights {
		p.weights[i] /= total
	}
	return p
}

// sample draws one value from the mixture, truncated to [lo, hi].
//
// Truncation uses inverse-CDF sampling within the chosen component, which
// terminates in constant time — a rejection loop can stall when the component
// sits far outside the box.
func (p *parzen) sample(rng *rand.Rand) float64 {
	u := rng.Float64()
	acc := 0.0
	idx := len(p.weights) - 1
	for i, w := range p.weights {
		acc += w
		if u <= acc {
			idx = i
			break
		}
	}
	mu, sigma := p.mus[idx], p.sigmas[idx]
	loCDF := normalCDF((p.lo - mu) / sigma)
	hiCDF := normalCDF((p.hi - mu) / sigma)
	if hiCDF <= loCDF {
		return mu
	}
	q := loCDF + rng.Float64()*(hiCDF-loCDF)
	v := mu + sigma*normalPPF(q)
	return math.Max(p.lo, math.Min(p.hi, v))
}

// logPDF returns the mixture's log density at x.
func (p *parzen) logPDF(x float64) float64 {
	logs := make([]float64, 0, len(p.mus))
	for i := range p.mus {
		mu, sigma := p.mus[i], p.sigmas[i]
		z := (x - mu) / sigma
		norm := normalCDF((p.hi-mu)/sigma) - normalCDF((p.lo-mu)/sigma)
		if norm <= 0 {
			continue
		}
		logComponent := -0.5*z*z - math.Log(sigma) - 0.5*math.Log(2.0*math.Pi) - math.Log(norm)
		logs = append(logs, math.Log(p.weights[i])+logComponent)
	}
	return logSumExp(logs)
}
