// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from VMAFx/vmafx pkg/prefilter/tpe.go at 73c76b24a.

package tpe

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestSampler_Convergence1DQuadratic(t *testing.T) {
	t.Parallel()

	dims := []Dimension{
		{Name: "x", Kind: KindContinuous, Lo: -10.0, Hi: 10.0},
	}
	cfg := DefaultConfig()
	cfg.StartupTrials = 5
	cfg.EICandidates = 24

	sampler, err := NewSampler(dims, cfg, 42)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}

	const target = 2.5
	for trial := range 60 {
		sugg, err := sampler.Suggest()
		if err != nil {
			t.Fatalf("trial %d Suggest: %v", trial, err)
		}
		x := sugg["x"]
		obj := (x - target) * (x - target)
		if err := sampler.Observe(sugg, obj); err != nil {
			t.Fatalf("trial %d Observe: %v", trial, err)
		}
	}

	bestParams, bestObj, ok := sampler.Best()
	if !ok {
		t.Fatal("expected Best() to return true after observations")
	}
	if bestObj > 0.05 {
		t.Errorf("bestObj = %g, want < 0.05", bestObj)
	}
	if diff := math.Abs(bestParams["x"] - target); diff > 0.25 {
		t.Errorf("bestParam x = %g, diff %g from target %g", bestParams["x"], diff, target)
	}
}

func TestSampler_ValidationErrors_Dimensions(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	tests := []struct {
		name string
		dims []Dimension
	}{
		{name: "empty dims", dims: nil},
		{name: "empty slice", dims: []Dimension{}},
		{name: "empty name", dims: []Dimension{{Name: "", Kind: KindContinuous, Lo: 0, Hi: 1}}},
		{name: "duplicate name", dims: []Dimension{
			{Name: "a", Kind: KindContinuous, Lo: 0, Hi: 1},
			{Name: "a", Kind: KindContinuous, Lo: 0, Hi: 1},
		}},
		{name: "Lo NaN", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: math.NaN(), Hi: 1}}},
		{name: "Lo +Inf", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: math.Inf(1), Hi: 1}}},
		{name: "Lo -Inf", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: math.Inf(-1), Hi: 1}}},
		{name: "Hi NaN", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: 0, Hi: math.NaN()}}},
		{name: "Hi +Inf", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: 0, Hi: math.Inf(1)}}},
		{name: "Hi -Inf", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: 0, Hi: math.Inf(-1)}}},
		{name: "Lo == Hi", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: 5, Hi: 5}}},
		{name: "Lo > Hi", dims: []Dimension{{Name: "x", Kind: KindContinuous, Lo: 6, Hi: 5}}},
		{name: "unknown kind", dims: []Dimension{{Name: "x", Kind: "invalid", Lo: 0, Hi: 1}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewSampler(tc.dims, cfg, 1)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}

	t.Run("exceed max 64 dims", func(t *testing.T) {
		t.Parallel()
		manyDims := make([]Dimension, 65)
		for i := range manyDims {
			manyDims[i] = Dimension{
				Name: fmt.Sprintf("d%d", i),
				Kind: KindContinuous,
				Lo:   0,
				Hi:   1,
			}
		}
		if _, err := NewSampler(manyDims, cfg, 1); err == nil {
			t.Fatal("expected error for 65 dimensions, got nil")
		}
	})
}

func TestSampler_ValidationErrors_Config(t *testing.T) {
	t.Parallel()

	dims := []Dimension{{Name: "x", Kind: KindContinuous, Lo: 0, Hi: 10}}
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "StartupTrials < 0", cfg: Config{StartupTrials: -1, EICandidates: 24, Gamma: 0.1, PriorWeight: 1}},
		{name: "StartupTrials > 1000", cfg: Config{StartupTrials: 1001, EICandidates: 24, Gamma: 0.1, PriorWeight: 1}},
		{name: "EICandidates < 1", cfg: Config{StartupTrials: 10, EICandidates: 0, Gamma: 0.1, PriorWeight: 1}},
		{name: "EICandidates > 256", cfg: Config{StartupTrials: 10, EICandidates: 257, Gamma: 0.1, PriorWeight: 1}},
		{name: "Gamma <= 0", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: 0.0, PriorWeight: 1}},
		{name: "Gamma negative", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: -0.1, PriorWeight: 1}},
		{name: "Gamma >= 1.0", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: 1.0, PriorWeight: 1}},
		{name: "Gamma NaN", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: math.NaN(), PriorWeight: 1}},
		{name: "Gamma Inf", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: math.Inf(1), PriorWeight: 1}},
		{name: "PriorWeight <= 0", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: 0.1, PriorWeight: 0.0}},
		{name: "PriorWeight NaN", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: 0.1, PriorWeight: math.NaN()}},
		{name: "PriorWeight Inf", cfg: Config{StartupTrials: 10, EICandidates: 24, Gamma: 0.1, PriorWeight: math.Inf(1)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewSampler(dims, tc.cfg, 1); err == nil {
				t.Fatalf("expected error for config case %q, got nil", tc.name)
			}
		})
	}
}

func TestSampler_ValidationErrors_Observe(t *testing.T) {
	t.Parallel()

	dims := []Dimension{
		{Name: "a", Kind: KindContinuous, Lo: 0, Hi: 10},
		{Name: "b", Kind: KindInt, Lo: 1, Hi: 5},
	}

	tests := []struct {
		name   string
		params map[string]float64
		obj    float64
	}{
		{name: "obj NaN", params: map[string]float64{"a": 1, "b": 2}, obj: math.NaN()},
		{name: "obj +Inf", params: map[string]float64{"a": 1, "b": 2}, obj: math.Inf(1)},
		{name: "obj -Inf", params: map[string]float64{"a": 1, "b": 2}, obj: math.Inf(-1)},
		{name: "missing param b", params: map[string]float64{"a": 1}, obj: 0.5},
		{name: "unknown param c", params: map[string]float64{"a": 1, "b": 2, "c": 3}, obj: 0.5},
		{name: "param a NaN", params: map[string]float64{"a": math.NaN(), "b": 2}, obj: 0.5},
		{name: "param a +Inf", params: map[string]float64{"a": math.Inf(1), "b": 2}, obj: 0.5},
		{name: "param b -Inf", params: map[string]float64{"a": 1, "b": math.Inf(-1)}, obj: 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sampler, err := NewSampler(dims, DefaultConfig(), 1)
			if err != nil {
				t.Fatalf("NewSampler: %v", err)
			}
			if err := sampler.Observe(tc.params, tc.obj); err == nil {
				t.Fatalf("expected Observe error for %q, got nil", tc.name)
			}
		})
	}
}

func TestSampler_Boundary_ObservationCap(t *testing.T) {
	t.Parallel()

	dims := []Dimension{{Name: "x", Kind: KindContinuous, Lo: 0, Hi: 1}}
	sampler, err := NewSampler(dims, DefaultConfig(), 1)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}

	params := map[string]float64{"x": 0.5}
	for i := range 10000 {
		if err := sampler.Observe(params, float64(i)); err != nil {
			t.Fatalf("unexpected Observe error at i=%d: %v", i, err)
		}
	}

	if err := sampler.Observe(params, 10001); err == nil {
		t.Fatal("expected error when observation cap of 10000 reached, got nil")
	}
}

func TestSampler_Boundary_StartupTrials(t *testing.T) {
	t.Parallel()

	dims := []Dimension{{Name: "x", Kind: KindContinuous, Lo: 0, Hi: 1}}

	t.Run("StartupTrials=0", func(t *testing.T) {
		t.Parallel()
		testStartupTrialsZero(t, dims)
	})

	t.Run("StartupTrials=1000", func(t *testing.T) {
		t.Parallel()
		testStartupTrialsMax(t, dims)
	})
}

func testStartupTrialsZero(t *testing.T, dims []Dimension) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.StartupTrials = 0
	s, err := NewSampler(dims, cfg, 42)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	p, suggErr := s.Suggest()
	if suggErr != nil {
		t.Fatalf("Suggest: %v", suggErr)
	}
	if obsErr := s.Observe(p, 1.0); obsErr != nil {
		t.Fatalf("Observe: %v", obsErr)
	}
	p2, suggErr2 := s.Suggest()
	if suggErr2 != nil {
		t.Fatalf("Suggest after 1 obs: %v", suggErr2)
	}
	if obsErr2 := s.Observe(p2, 0.5); obsErr2 != nil {
		t.Fatalf("Observe 2: %v", obsErr2)
	}
	if _, suggErr3 := s.Suggest(); suggErr3 != nil {
		t.Fatalf("Suggest after 2 obs: %v", suggErr3)
	}
}

func testStartupTrialsMax(t *testing.T, dims []Dimension) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.StartupTrials = 1000
	s, err := NewSampler(dims, cfg, 42)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	if _, err := s.Suggest(); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
}

func TestQuantise_BoundariesAndEdgeLattice(t *testing.T) {
	t.Parallel()

	contDim := Dimension{Name: "c", Kind: KindContinuous, Lo: 1.0, Hi: 5.0}
	if got := quantise(0.5, contDim); got != 1.0 {
		t.Errorf("cont underflow: got %v, want 1.0", got)
	}
	if got := quantise(5.5, contDim); got != 5.0 {
		t.Errorf("cont overflow: got %v, want 5.0", got)
	}
	if got := quantise(2.7, contDim); got != 2.7 {
		t.Errorf("cont interior: got %v, want 2.7", got)
	}

	intDim := Dimension{Name: "i", Kind: KindInt, Lo: 1.0, Hi: 5.0}
	if got := quantise(0.2, intDim); got != 1.0 {
		t.Errorf("int underflow: got %v, want 1.0", got)
	}
	if got := quantise(5.8, intDim); got != 5.0 {
		t.Errorf("int overflow: got %v, want 5.0", got)
	}
	if got := quantise(2.4, intDim); got != 2.0 {
		t.Errorf("int round down: got %v, want 2.0", got)
	}
	if got := quantise(2.6, intDim); got != 3.0 {
		t.Errorf("int round up: got %v, want 3.0", got)
	}

	boolDim := Dimension{Name: "b", Kind: KindBool, Lo: 0.0, Hi: 1.0}
	if got := quantise(0.8, boolDim); got != 1.0 {
		t.Errorf("bool round: got %v, want 1.0", got)
	}

	enumDim := Dimension{Name: "e", Kind: KindEnum, Lo: 0.0, Hi: 4.0}
	if got := quantise(3.1, enumDim); got != 3.0 {
		t.Errorf("enum round: got %v, want 3.0", got)
	}
}

func TestSampler_Determinism(t *testing.T) {
	t.Parallel()

	dims := []Dimension{
		{Name: "x", Kind: KindContinuous, Lo: -5.0, Hi: 5.0},
		{Name: "i", Kind: KindInt, Lo: 0.0, Hi: 10.0},
	}
	cfg := DefaultConfig()

	s1, err := NewSampler(dims, cfg, 999)
	if err != nil {
		t.Fatalf("NewSampler s1: %v", err)
	}
	s2, err := NewSampler(dims, cfg, 999)
	if err != nil {
		t.Fatalf("NewSampler s2: %v", err)
	}
	sDiff, err := NewSampler(dims, cfg, 1000)
	if err != nil {
		t.Fatalf("NewSampler sDiff: %v", err)
	}

	differ := false
	for step := range 20 {
		diff := stepDeterminism(t, step, s1, s2, sDiff)
		if diff {
			differ = true
		}
	}
	if !differ {
		t.Error("expected different seeds to produce different proposals")
	}
}

func stepDeterminism(t *testing.T, step int, s1, s2, sDiff *Sampler) bool {
	t.Helper()
	p1, err := s1.Suggest()
	if err != nil {
		t.Fatalf("s1 Suggest: %v", err)
	}
	p2, err := s2.Suggest()
	if err != nil {
		t.Fatalf("s2 Suggest: %v", err)
	}
	pDiff, err := sDiff.Suggest()
	if err != nil {
		t.Fatalf("sDiff Suggest: %v", err)
	}

	if p1["x"] != p2["x"] || p1["i"] != p2["i"] {
		t.Fatalf("step %d: s1 and s2 diverged: %v vs %v", step, p1, p2)
	}

	obj := p1["x"]*p1["x"] + p1["i"]
	_ = s1.Observe(p1, obj)
	_ = s2.Observe(p2, obj)
	_ = sDiff.Observe(pDiff, obj)

	return p1["x"] != pDiff["x"]
}

func TestSampler_Best(t *testing.T) {
	t.Parallel()

	dims := []Dimension{{Name: "x", Kind: KindContinuous, Lo: 0, Hi: 10}}
	s, err := NewSampler(dims, DefaultConfig(), 1)
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}

	testBestEmpty(t, s)
	testBestObserved(t, s)
}

func testBestEmpty(t *testing.T, s *Sampler) {
	t.Helper()
	params, obj, ok := s.Best()
	if ok || params != nil || obj != 0 {
		t.Errorf("before obs: got ok=%v, params=%v, obj=%v", ok, params, obj)
	}
	if s.Trials() != 0 {
		t.Errorf("Trials() = %d, want 0", s.Trials())
	}
}

func testBestObserved(t *testing.T, s *Sampler) {
	t.Helper()
	_ = s.Observe(map[string]float64{"x": 1.0}, 10.0)
	_ = s.Observe(map[string]float64{"x": 3.0}, 2.5)
	_ = s.Observe(map[string]float64{"x": 5.0}, 7.0)

	if s.Trials() != 3 {
		t.Errorf("Trials() = %d, want 3", s.Trials())
	}

	bestParams, bestObj, ok := s.Best()
	if !ok {
		t.Fatal("expected Best() ok=true")
	}
	if bestObj != 2.5 {
		t.Errorf("bestObj = %g, want 2.5", bestObj)
	}
	if bestParams["x"] != 3.0 {
		t.Errorf("bestParams['x'] = %g, want 3.0", bestParams["x"])
	}

	bestParams["x"] = 999.0
	againParams, _, _ := s.Best()
	if againParams["x"] != 3.0 {
		t.Errorf("internal state mutated: got %g, want 3.0", againParams["x"])
	}
}

func TestMath_CornerCases(t *testing.T) {
	t.Parallel()

	// logSumExp
	if v := logSumExp(nil); !math.IsInf(v, -1) {
		t.Errorf("logSumExp(nil) = %v, want -Inf", v)
	}
	if v := logSumExp([]float64{math.Inf(-1), math.Inf(-1)}); !math.IsInf(v, -1) {
		t.Errorf("logSumExp(-Inf, -Inf) = %v, want -Inf", v)
	}

	// normalPPF bounds
	if v := normalPPF(0.0); !math.IsInf(v, -1) {
		t.Errorf("normalPPF(0) = %v, want -Inf", v)
	}
	if v := normalPPF(-1.0); !math.IsInf(v, -1) {
		t.Errorf("normalPPF(-1) = %v, want -Inf", v)
	}
	if v := normalPPF(1.0); !math.IsInf(v, 1) {
		t.Errorf("normalPPF(1) = %v, want +Inf", v)
	}
	if v := normalPPF(2.0); !math.IsInf(v, 1) {
		t.Errorf("normalPPF(2) = %v, want +Inf", v)
	}

	// parzen corner cases
	dim := Dimension{Name: "x", Kind: KindContinuous, Lo: 2.0, Hi: 2.0}
	p := newParzen(nil, dim, 1.0)
	if p.sigmas[0] != 1.0 {
		t.Errorf("expected sigma fallback for span <= 0")
	}

	// parzen truncated sample degenerate
	pDegenerate := &parzen{
		mus:     []float64{0.0},
		sigmas:  []float64{0.1},
		weights: []float64{1.0},
		lo:      10.0,
		hi:      10.0,
	}
	r := rand.New(rand.NewSource(1)) //nolint:gosec // G404: test RNG
	if v := pDegenerate.sample(r); v != 0.0 {
		t.Errorf("degenerate sample = %v, want 0.0", v)
	}

	// parzen logPDF norm <= 0
	if density := pDegenerate.logPDF(0.0); !math.IsInf(density, -1) {
		t.Errorf("degenerate logPDF = %v, want -Inf", density)
	}
}
