// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from VMAFx/vmafx pkg/prefilter/tpe.go at 73c76b24a.

package tpe

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// maxArchULP is the measured amd64-vs-arm64 maximum after FMA-free rounding (2026-10-17).
const maxArchULP uint64 = 24

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

type trajectoryDimItem struct {
	Name string  `json:"name"`
	Kind string  `json:"kind"`
	Lo   float64 `json:"lo"`
	Hi   float64 `json:"hi"`
}

type trajectoryConfigItem struct {
	StartupTrials int     `json:"startup_trials"`
	EICandidates  int     `json:"ei_candidates"`
	Gamma         float64 `json:"gamma"`
	PriorWeight   float64 `json:"prior_weight"`
}

type trajectoryStepItem struct {
	Step       int                `json:"step"`
	Suggestion map[string]float64 `json:"suggestion"`
	Objective  float64            `json:"objective"`
}

type trajectoryItem struct {
	Seed   int64                `json:"seed"`
	Dims   []trajectoryDimItem  `json:"dims"`
	Config trajectoryConfigItem `json:"config"`
	Steps  []trajectoryStepItem `json:"steps"`
}

type provenanceData struct {
	GeneratedBy  string  `json:"generated_by"`
	GoVersion    string  `json:"go_version"`
	Emulator     string  `json:"emulator"`
	Seeds        []int64 `json:"seeds"`
	SourceGolden string  `json:"source_golden"`
}

type trajectoryConformanceData struct {
	Provenance   *provenanceData  `json:"provenance,omitempty"`
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

	fileName := "tpe_trajectory.json"
	if runtime.GOARCH == "arm64" {
		fileName = "tpe_trajectory_arm64.json"
	}
	conf := loadTrajectoryGolden(t, fileName)

	for _, traj := range conf.Trajectories {
		replayTrajectory(t, traj)
	}
}

func TestConformance_ArchGoldenBound(t *testing.T) {
	t.Parallel()

	base := loadTrajectoryGolden(t, "tpe_trajectory.json")
	arm := loadTrajectoryGolden(t, "tpe_trajectory_arm64.json")

	if len(base.Trajectories) != len(arm.Trajectories) {
		t.Fatalf("trajectory count mismatch: base=%d arm=%d", len(base.Trajectories), len(arm.Trajectories))
	}

	for i := range base.Trajectories {
		compareTrajectoryBound(t, base.Trajectories[i], arm.Trajectories[i])
	}
}

//nolint:paralleltest // WHY: generator test mutates testdata files on disk.
func TestRegenARM64(t *testing.T) {
	if os.Getenv("TPE_REGEN_ARM64") != "1" {
		t.Skip("skipping ARM64 regeneration; set TPE_REGEN_ARM64=1 to run")
	}
	if runtime.GOARCH != "arm64" {
		t.Skip("regeneration only supported on GOARCH=arm64")
	}

	base := loadTrajectoryGolden(t, "tpe_trajectory.json")
	armTrajectories := make([]trajectoryItem, len(base.Trajectories))
	seeds := make([]int64, len(base.Trajectories))

	for i, traj := range base.Trajectories {
		seeds[i] = traj.Seed
		armTrajectories[i] = generateARM64Trajectory(t, traj)
	}

	provenance := &provenanceData{
		GeneratedBy:  "cd ai/tpe && TPE_REGEN_ARM64=1 GOWORK=off GOARCH=arm64 go test -count=1 -run TestRegenARM64 ./",
		GoVersion:    runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		Emulator:     "qemu-aarch64 binfmt",
		Seeds:        seeds,
		SourceGolden: "tpe_trajectory.json",
	}

	arm64Data := trajectoryConformanceData{
		Provenance:   provenance,
		Trajectories: armTrajectories,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(arm64Data); err != nil {
		t.Fatalf("marshaling arm64 conformance data: %v", err)
	}

	outPath := filepath.Join("testdata", "conformance", "tpe_trajectory_arm64.json")
	if err := os.WriteFile(outPath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("writing %s: %v", outPath, err)
	}
}

func loadTrajectoryGolden(t *testing.T, filename string) trajectoryConformanceData {
	t.Helper()
	path := filepath.Join("testdata", "conformance", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", filename, err)
	}
	var conf trajectoryConformanceData
	if err := json.Unmarshal(data, &conf); err != nil {
		t.Fatalf("unmarshaling %s: %v", filename, err)
	}
	return conf
}

func replayTrajectory(t *testing.T, traj trajectoryItem) {
	t.Helper()
	sampler := newTrajectorySampler(t, traj)

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

func generateARM64Trajectory(t *testing.T, traj trajectoryItem) trajectoryItem {
	t.Helper()
	sampler := newTrajectorySampler(t, traj)

	steps := make([]trajectoryStepItem, len(traj.Steps))
	for i, step := range traj.Steps {
		gotSugg, err := sampler.Suggest()
		if err != nil {
			t.Fatalf("seed %d step %d: Suggest: %v", traj.Seed, step.Step, err)
		}
		steps[i] = trajectoryStepItem{
			Step:       step.Step,
			Suggestion: gotSugg,
			Objective:  step.Objective,
		}
		if err := sampler.Observe(gotSugg, step.Objective); err != nil {
			t.Fatalf("seed %d step %d: Observe: %v", traj.Seed, step.Step, err)
		}
	}

	out := traj
	out.Steps = steps
	return out
}

func compareTrajectoryBound(t *testing.T, base, arm trajectoryItem) {
	t.Helper()
	if base.Seed != arm.Seed {
		t.Fatalf("seed mismatch: base=%d arm=%d", base.Seed, arm.Seed)
	}
	compareDimsAndConfig(t, base, arm)
	if len(base.Steps) != len(arm.Steps) {
		t.Fatalf("seed %d: step count mismatch: base=%d arm=%d", base.Seed, len(base.Steps), len(arm.Steps))
	}
	for i := range base.Steps {
		compareStepBound(t, base.Seed, base.Steps[i], arm.Steps[i])
	}
}

func compareDimsAndConfig(t *testing.T, base, arm trajectoryItem) {
	t.Helper()
	if len(base.Dims) != len(arm.Dims) {
		t.Fatalf("seed %d: dims count mismatch: base=%d arm=%d", base.Seed, len(base.Dims), len(arm.Dims))
	}
	for i := range base.Dims {
		if base.Dims[i] != arm.Dims[i] {
			t.Fatalf("seed %d: dim %d mismatch: base=%+v arm=%+v", base.Seed, i, base.Dims[i], arm.Dims[i])
		}
	}
	if base.Config != arm.Config {
		t.Fatalf("seed %d: config mismatch: base=%+v arm=%+v", base.Seed, base.Config, arm.Config)
	}
}

func compareStepBound(t *testing.T, seed int64, base, arm trajectoryStepItem) {
	t.Helper()
	if base.Step != arm.Step {
		t.Fatalf("seed %d: step mismatch: base=%d arm=%d", seed, base.Step, arm.Step)
	}
	if math.Float64bits(base.Objective) != math.Float64bits(arm.Objective) {
		t.Fatalf("seed %d step %d: objective mismatch: base=%g arm=%g", seed, base.Step, base.Objective, arm.Objective)
	}
	if len(base.Suggestion) != len(arm.Suggestion) {
		t.Fatalf("seed %d step %d: param count mismatch: base=%d arm=%d",
			seed, base.Step, len(base.Suggestion), len(arm.Suggestion))
	}
	for k, wantVal := range base.Suggestion {
		gotVal, ok := arm.Suggestion[k]
		if !ok {
			t.Fatalf("seed %d step %d: arm missing param %q", seed, base.Step, k)
		}
		dist, ok := ulpDistance(wantVal, gotVal)
		if !ok {
			t.Errorf("seed %d step %d param %s: sign mismatch (base=%g, arm=%g)", seed, base.Step, k, wantVal, gotVal)
			continue
		}
		if dist > maxArchULP {
			t.Errorf("seed %d step %d param %s: ULP distance %d exceeds bound %d (base=%g, arm=%g)",
				seed, base.Step, k, dist, maxArchULP, wantVal, gotVal)
		}
	}
}

func ulpDistance(a, b float64) (uint64, bool) {
	if math.IsNaN(a) || math.IsNaN(b) {
		return 0, false
	}
	if a == 0 && b == 0 {
		return 0, true
	}
	bitsA := math.Float64bits(a)
	bitsB := math.Float64bits(b)
	if (bitsA >> 63) != (bitsB >> 63) {
		return 0, false
	}
	if bitsA >= bitsB {
		return bitsA - bitsB, true
	}
	return bitsB - bitsA, true
}

func newTrajectorySampler(t *testing.T, traj trajectoryItem) *Sampler {
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
	return sampler
}
