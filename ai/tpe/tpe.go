// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from VMAFx/vmafx pkg/prefilter/tpe.go at 73c76b24a.

// Package tpe provides a Tree-structured Parzen Estimator (TPE) for
// hyperparameter search and black-box optimization over mixed continuous
// and discrete parameter spaces.
package tpe

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- seeded PRNG for reproducible hyperparameter search, not security
	"sort"
)

// Kind classifies the domain of a search dimension.
type Kind string

const (
	// KindContinuous is a continuous real-valued dimension.
	KindContinuous Kind = "continuous"
	// KindFloat is an alias for KindContinuous matching AVOption float knobs.
	KindFloat Kind = "float"
	// KindInt is a discrete integer dimension.
	KindInt Kind = "int"
	// KindBool is a binary 0/1 integer dimension.
	KindBool Kind = "bool"
	// KindEnum is a categorical dimension represented by integer indices.
	KindEnum Kind = "enum"
)

// Dimension is one bounded axis of the search space.
type Dimension struct {
	Name string
	Kind Kind
	Lo   float64
	Hi   float64
}

// IsIntegral reports whether the dimension only takes integer values.
func (d Dimension) IsIntegral() bool {
	return d.Kind == KindInt || d.Kind == KindBool || d.Kind == KindEnum
}

// Config tunes the sampler hyperparameters.
type Config struct {
	// StartupTrials is how many proposals are drawn uniformly before the
	// Parzen mixture model takes over.
	StartupTrials int
	// EICandidates is how many candidates are sampled per proposal step.
	EICandidates int
	// Gamma is the quantile threshold partitioning trials into good (l)
	// and remaining (g) sets.
	Gamma float64
	// PriorWeight is the relative mass of the uniform box prior component.
	PriorWeight float64
}

// DefaultConfig returns the standard Optuna-compatible default parameters.
func DefaultConfig() Config {
	return Config{
		StartupTrials: 10,
		EICandidates:  24,
		Gamma:         0.10,
		PriorWeight:   1.0,
	}
}

// observation is one completed trial recording parameters and achieved objective.
type observation struct {
	params    map[string]float64
	objective float64
}

// Sampler proposes configurations in a bounded multi-dimensional search space.
// It is not safe for concurrent use.
type Sampler struct {
	dims     []Dimension
	dimIndex map[string]int
	cfg      Config
	rng      *rand.Rand
	obs      []observation
}

// NewSampler constructs a validated TPE sampler with the given dimensions and configuration.
func NewSampler(dims []Dimension, cfg Config, seed int64) (*Sampler, error) {
	if err := validateDimensions(dims); err != nil {
		return nil, err
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	copiedDims := make([]Dimension, len(dims))
	copy(copiedDims, dims)
	dimIndex := make(map[string]int, len(dims))
	for i, d := range copiedDims {
		dimIndex[d.Name] = i
	}

	return &Sampler{
		dims:     copiedDims,
		dimIndex: dimIndex,
		cfg:      cfg,
		//nolint:gosec,forbidigo // G404: deterministic pseudo-random stream for reproducible hyperparameter search
		rng: rand.New(rand.NewSource(seed)), // #nosec G404 -- seeded PRNG for reproducible hyperparameter search, not security
		obs: make([]observation, 0, 32),
	}, nil
}

func validateDimensions(dims []Dimension) error {
	if len(dims) == 0 {
		return errors.New("ai/tpe: new sampler: dims must not be empty")
	}
	if len(dims) > 64 {
		return fmt.Errorf("ai/tpe: new sampler: dim count %d exceeds cap of 64", len(dims))
	}
	seen := make(map[string]struct{}, len(dims))
	for _, d := range dims {
		if err := validateDimension(d, seen); err != nil {
			return err
		}
		seen[d.Name] = struct{}{}
	}
	return nil
}

func validateDimension(d Dimension, seen map[string]struct{}) error {
	if d.Name == "" {
		return errors.New("ai/tpe: new sampler: dimension name must not be empty")
	}
	if _, ok := seen[d.Name]; ok {
		return fmt.Errorf("ai/tpe: new sampler: duplicate dimension name %q", d.Name)
	}
	if math.IsNaN(d.Lo) || math.IsInf(d.Lo, 0) {
		return fmt.Errorf("ai/tpe: new sampler: dimension %q lower bound %v is NaN or Inf", d.Name, d.Lo)
	}
	if math.IsNaN(d.Hi) || math.IsInf(d.Hi, 0) {
		return fmt.Errorf("ai/tpe: new sampler: dimension %q upper bound %v is NaN or Inf", d.Name, d.Hi)
	}
	if d.Lo >= d.Hi {
		return fmt.Errorf("ai/tpe: new sampler: dimension %q Lo %v >= Hi %v", d.Name, d.Lo, d.Hi)
	}
	switch d.Kind {
	case KindContinuous, KindFloat, KindInt, KindBool, KindEnum:
		return nil
	default:
		return fmt.Errorf("ai/tpe: new sampler: dimension %q has unknown kind %q", d.Name, d.Kind)
	}
}

func validateConfig(cfg Config) error {
	if err := validateConfigCounts(cfg); err != nil {
		return err
	}
	return validateConfigWeights(cfg)
}

func validateConfigCounts(cfg Config) error {
	if cfg.StartupTrials < 0 || cfg.StartupTrials > 1000 {
		return fmt.Errorf("ai/tpe: new sampler: StartupTrials %d out of range [0, 1000]", cfg.StartupTrials)
	}
	if cfg.EICandidates < 1 || cfg.EICandidates > 256 {
		return fmt.Errorf("ai/tpe: new sampler: EICandidates %d out of range [1, 256]", cfg.EICandidates)
	}
	return nil
}

func validateConfigWeights(cfg Config) error {
	if math.IsNaN(cfg.Gamma) || math.IsInf(cfg.Gamma, 0) || cfg.Gamma <= 0.0 || cfg.Gamma >= 1.0 {
		return fmt.Errorf("ai/tpe: new sampler: Gamma %v out of range (0, 1)", cfg.Gamma)
	}
	if math.IsNaN(cfg.PriorWeight) || math.IsInf(cfg.PriorWeight, 0) || cfg.PriorWeight <= 0.0 {
		return fmt.Errorf("ai/tpe: new sampler: PriorWeight %v must be positive", cfg.PriorWeight)
	}
	return nil
}

// Observe records a evaluated parameter point and its objective value.
func (s *Sampler) Observe(params map[string]float64, objective float64) error {
	if len(s.obs) >= 10000 {
		return errors.New("ai/tpe: observe: observation cap of 10000 reached")
	}
	if math.IsNaN(objective) || math.IsInf(objective, 0) {
		return fmt.Errorf("ai/tpe: observe: objective %v is NaN or Inf", objective)
	}
	for _, d := range s.dims {
		v, ok := params[d.Name]
		if !ok {
			return fmt.Errorf("ai/tpe: observe: missing parameter %q", d.Name)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("ai/tpe: observe: parameter %q value %v is NaN or Inf", d.Name, v)
		}
	}
	for k := range params {
		if _, ok := s.dimIndex[k]; !ok {
			return fmt.Errorf("ai/tpe: observe: unknown parameter %q", k)
		}
	}
	copied := make(map[string]float64, len(params))
	maps.Copy(copied, params)
	s.obs = append(s.obs, observation{params: copied, objective: objective})
	return nil
}

// Trials returns the total count of observations held by the sampler.
func (s *Sampler) Trials() int {
	return len(s.obs)
}

// Suggest proposes the next parameter evaluation point.
func (s *Sampler) Suggest() (map[string]float64, error) {
	if len(s.obs) < s.cfg.StartupTrials {
		return s.sampleUniform(), nil
	}
	below, above := s.split()
	if len(below) == 0 || len(above) == 0 {
		return s.sampleUniform(), nil
	}

	proposal := make(map[string]float64, len(s.dims))
	for _, dim := range s.dims {
		lEst := newParzen(observedValues(below, dim.Name), dim, s.cfg.PriorWeight)
		gEst := newParzen(observedValues(above, dim.Name), dim, s.cfg.PriorWeight)

		bestScore := math.Inf(-1)
		best := dim.Lo
		for range s.cfg.EICandidates {
			candidate := lEst.sample(s.rng)
			score := lEst.logPDF(candidate) - gEst.logPDF(candidate)
			if score > bestScore {
				bestScore, best = score, candidate
			}
		}
		proposal[dim.Name] = quantise(best, dim)
	}
	return proposal, nil
}

// Best returns the parameter set achieving the minimal objective observed so far.
func (s *Sampler) Best() (map[string]float64, float64, bool) {
	if len(s.obs) == 0 {
		return nil, 0, false
	}
	bestIdx := 0
	bestObj := s.obs[0].objective
	for i := 1; i < len(s.obs); i++ {
		if s.obs[i].objective < bestObj {
			bestObj = s.obs[i].objective
			bestIdx = i
		}
	}
	copied := make(map[string]float64, len(s.obs[bestIdx].params))
	maps.Copy(copied, s.obs[bestIdx].params)
	return copied, bestObj, true
}

func (s *Sampler) sampleUniform() map[string]float64 {
	out := make(map[string]float64, len(s.dims))
	for _, dim := range s.dims {
		out[dim.Name] = quantise(dim.Lo+s.rng.Float64()*(dim.Hi-dim.Lo), dim)
	}
	return out
}

func (s *Sampler) split() (below, above []observation) {
	sorted := make([]observation, len(s.obs))
	copy(sorted, s.obs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].objective < sorted[j].objective
	})
	n := len(sorted)
	nBelow := max(min(int(math.Ceil(s.cfg.Gamma*float64(n))), 25), 1)
	if nBelow >= n {
		nBelow = n - 1
	}
	if nBelow < 1 {
		return nil, nil
	}
	return sorted[:nBelow], sorted[nBelow:]
}

func observedValues(obs []observation, name string) []float64 {
	out := make([]float64, 0, len(obs))
	for _, o := range obs {
		if v, ok := o.params[name]; ok {
			out = append(out, v)
		}
	}
	return out
}

func quantise(v float64, dim Dimension) float64 {
	if v < dim.Lo {
		v = dim.Lo
	}
	if v > dim.Hi {
		v = dim.Hi
	}
	if dim.IsIntegral() {
		v = math.Round(v)
		if v < dim.Lo {
			v = math.Ceil(dim.Lo)
		}
		if v > dim.Hi {
			v = math.Floor(dim.Hi)
		}
	}
	return v
}
