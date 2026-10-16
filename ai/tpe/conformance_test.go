// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from VMAFx/vmafx pkg/prefilter/tpe.go at 73c76b24a.

package tpe

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

type mathConformanceData struct {
	NormalCDF []struct {
		Z      float64 `json:"z"`
		Result float64 `json:"result"`
	} `json:"normal_cdf"`
	NormalPPF []struct {
		P      float64 `json:"p"`
		Result float64 `json:"result"`
	} `json:"normal_ppf"`
	LogSumExp []struct {
		Values []float64 `json:"values"`
		Result float64   `json:"result"`
	} `json:"log_sum_exp"`
}

type trajectoryItem struct {
	Seed int64 `json:"seed"`
	Dims []struct {
		Name string  `json:"name"`
		Kind string  `json:"kind"`
		Lo   float64 `json:"lo"`
		Hi   float64 `json:"hi"`
	} `json:"dims"`
	Config struct {
		StartupTrials int     `json:"startup_trials"`
		EICandidates  int     `json:"ei_candidates"`
		Gamma         float64 `json:"gamma"`
		PriorWeight   float64 `json:"prior_weight"`
	} `json:"config"`
	Steps []trajectoryStepItem `json:"steps"`
}

type trajectoryStepItem struct {
	Step       int                `json:"step"`
	Suggestion map[string]float64 `json:"suggestion"`
	Objective  float64            `json:"objective"`
}

type trajectoryConformanceData struct {
	Trajectories []trajectoryItem `json:"trajectories"`
}

func TestConformance_Math(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "conformance", "tpe_math.json"))
	if err != nil {
		t.Fatalf("reading tpe_math.json: %v", err)
	}

	var conf mathConformanceData
	if err := json.Unmarshal(data, &conf); err != nil {
		t.Fatalf("unmarshaling tpe_math.json: %v", err)
	}

	for _, pt := range conf.NormalCDF {
		got := normalCDF(pt.Z)
		if math.Float64bits(got) != math.Float64bits(pt.Result) {
			t.Errorf("normalCDF(%g) = %g (bits 0x%016x), want %g (bits 0x%016x)",
				pt.Z, got, math.Float64bits(got), pt.Result, math.Float64bits(pt.Result))
		}
	}

	for _, pt := range conf.NormalPPF {
		got := normalPPF(pt.P)
		if math.Float64bits(got) != math.Float64bits(pt.Result) {
			t.Errorf("normalPPF(%g) = %g (bits 0x%016x), want %g (bits 0x%016x)",
				pt.P, got, math.Float64bits(got), pt.Result, math.Float64bits(pt.Result))
		}
	}

	for _, pt := range conf.LogSumExp {
		got := logSumExp(pt.Values)
		if math.Float64bits(got) != math.Float64bits(pt.Result) {
			t.Errorf("logSumExp(%v) = %g (bits 0x%016x), want %g (bits 0x%016x)",
				pt.Values, got, math.Float64bits(got), pt.Result, math.Float64bits(pt.Result))
		}
	}
}

func TestConformance_Trajectory(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "conformance", "tpe_trajectory.json"))
	if err != nil {
		t.Fatalf("reading tpe_trajectory.json: %v", err)
	}

	var conf trajectoryConformanceData
	if err := json.Unmarshal(data, &conf); err != nil {
		t.Fatalf("unmarshaling tpe_trajectory.json: %v", err)
	}

	for _, traj := range conf.Trajectories {
		replayTrajectory(t, traj)
	}
}

func replayTrajectory(t *testing.T, traj trajectoryItem) {
	t.Helper()
	dims := make([]Dimension, len(traj.Dims))
	for i, d := range traj.Dims {
		dims[i] = Dimension{
			Name: d.Name,
			Kind: Kind(d.Kind),
			Lo:   d.Lo,
			Hi:   d.Hi,
		}
	}
	cfg := Config{
		StartupTrials: traj.Config.StartupTrials,
		EICandidates:  traj.Config.EICandidates,
		Gamma:         traj.Config.Gamma,
		PriorWeight:   traj.Config.PriorWeight,
	}

	sampler, err := NewSampler(dims, cfg, traj.Seed)
	if err != nil {
		t.Fatalf("seed %d: NewSampler: %v", traj.Seed, err)
	}

	for _, step := range traj.Steps {
		replayTrajectoryStep(t, sampler, traj.Seed, step)
	}
}

func replayTrajectoryStep(t *testing.T, sampler *Sampler, seed int64, step trajectoryStepItem) {
	t.Helper()
	gotSugg, err := sampler.Suggest()
	if err != nil {
		t.Fatalf("seed %d step %d: Suggest: %v", seed, step.Step, err)
	}
	for k, wantVal := range step.Suggestion {
		gotVal, ok := gotSugg[k]
		if !ok {
			t.Fatalf("seed %d step %d: missing param %q", seed, step.Step, k)
		}
		if math.Float64bits(gotVal) != math.Float64bits(wantVal) {
			t.Errorf("seed %d step %d param %s = %g (bits 0x%016x), want %g (bits 0x%016x)",
				seed, step.Step, k, gotVal, math.Float64bits(gotVal), wantVal, math.Float64bits(wantVal))
		}
	}
	if err := sampler.Observe(gotSugg, step.Objective); err != nil {
		t.Fatalf("seed %d step %d: Observe: %v", seed, step.Step, err)
	}
}
