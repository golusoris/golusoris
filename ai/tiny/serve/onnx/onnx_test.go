// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package onnx_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/onnx"
	"github.com/golusoris/golusoris/internal/fileuri"
)

type runnerInstruction struct {
	ExitCode       int    `json:"exit_code"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	SleepMs        int    `json:"sleep_ms"`
	RecordArgvFile string `json:"record_argv_file"`
	EmptyStdout    bool   `json:"empty_stdout"`
}

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_RUNNER") == "1" {
		runHelperProcess()
		os.Exit(0)
	}
	if err := os.Setenv("GO_WANT_HELPER_RUNNER", "1"); err != nil {
		fmt.Fprintf(os.Stderr, "set helper env: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func runHelperProcess() {
	fs := flag.NewFlagSet("vmafx-ort-runner", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	model := fs.String("model", "", "model path")
	inputs := fs.String("inputs", "", "inputs JSON")
	inputName := fs.String("input-name", "", "input name")

	if err := fs.Parse(os.Args[1:]); err != nil || fs.NArg() != 0 || *model == "" || *inputs == "" {
		fmt.Fprintf(os.Stderr, "vmafx-ort-runner: usage error (inputName=%s)\n", *inputName)
		os.Exit(2)
	}

	var rawNums []float64
	if err := json.Unmarshal([]byte(*inputs), &rawNums); err != nil {
		fmt.Fprintf(os.Stderr, "vmafx-ort-runner: malformed inputs: %v\n", err)
		os.Exit(2)
	}

	executeInstruction(*model)
}

func executeInstruction(modelPath string) {
	var instr runnerInstruction
	data, err := os.ReadFile(modelPath)
	if err == nil {
		err = json.Unmarshal(data, &instr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "vmafx-ort-runner: read model: %v\n", err)
		os.Exit(1)
	}

	if instr.RecordArgvFile != "" {
		if err := os.WriteFile(instr.RecordArgvFile, []byte(strings.Join(os.Args[1:], "\n")), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "vmafx-ort-runner: record argv: %v\n", err)
			os.Exit(1)
		}
	}
	if instr.SleepMs > 0 {
		time.Sleep(time.Duration(instr.SleepMs) * time.Millisecond)
	}
	if instr.Stderr != "" {
		fmt.Fprint(os.Stderr, instr.Stderr)
	}
	if instr.ExitCode != 0 {
		os.Exit(instr.ExitCode)
	}
	if instr.EmptyStdout {
		return
	}
	if instr.Stdout != "" {
		fmt.Fprintln(os.Stdout, instr.Stdout)
		return
	}
	fmt.Fprintln(os.Stdout, "[1.0]")
}

func writeModelFixture(t *testing.T, instr runnerInstruction) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "model.onnx")
	data, err := json.Marshal(instr)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func newTestPredictor(opts onnx.Options) *onnx.Predictor {
	if opts.RunnerPath == "" {
		opts.RunnerPath = os.Args[0]
	}
	return onnx.NewPredictor(opts)
}

func TestPredictor_Positive_Regress(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[42.5]"})
	p := newTestPredictor(onnx.Options{})
	m := tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress, Format: tiny.FormatONNX}
	require.NoError(t, p.Load(t.Context(), m))

	got64, err := p.Predict(t.Context(), []float64{1.0, 2.0})
	require.NoError(t, err)
	require.InDelta(t, 42.5, got64.Value, 1e-6)
	require.Equal(t, []float64{42.5}, got64.Raw)

	got32, err := p.Predict(t.Context(), []float32{3.0, 4.0})
	require.NoError(t, err)
	require.InDelta(t, 42.5, got32.Value, 1e-6)
	require.Equal(t, []float64{42.5}, got32.Raw)
}

func TestPredictor_Positive_Classify(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[0.1, 0.9, 0.3]"})
	p := newTestPredictor(onnx.Options{})
	m := tiny.Model{
		URI:      modelPath,
		TaskKind: tiny.TaskClassify,
		Labels:   []string{"cat", "dog", "fox"},
		Format:   tiny.FormatONNX,
	}
	require.NoError(t, p.Load(t.Context(), m))

	got, err := p.Predict(t.Context(), []float64{0.5})
	require.NoError(t, err)
	require.Len(t, got.Labels, 3)
	require.Equal(t, "dog", got.Labels[0].Label)
	require.InDelta(t, float32(0.9), got.Labels[0].Score, 1e-6)
	require.Equal(t, "fox", got.Labels[1].Label)
	require.InDelta(t, float32(0.3), got.Labels[1].Score, 1e-6)
	require.Equal(t, "cat", got.Labels[2].Label)
	require.InDelta(t, float32(0.1), got.Labels[2].Score, 1e-6)
}

func TestPredictor_Positive_ClassifyTieBreaking(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[0.5, 0.5]"})
	p := newTestPredictor(onnx.Options{})
	m := tiny.Model{
		URI:      modelPath,
		TaskKind: tiny.TaskClassify,
		Labels:   []string{"b", "a"},
		Format:   tiny.FormatONNX,
	}
	require.NoError(t, p.Load(t.Context(), m))

	got, err := p.Predict(t.Context(), []float64{0.5})
	require.NoError(t, err)
	require.Len(t, got.Labels, 2)
	require.Equal(t, "a", got.Labels[0].Label)
	require.Equal(t, "b", got.Labels[1].Label)
}

func TestPredictor_Positive_Embed(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[0.25, 0.5, 0.75]"})
	p := newTestPredictor(onnx.Options{})
	m := tiny.Model{URI: modelPath, TaskKind: tiny.TaskEmbed}
	require.NoError(t, p.Load(t.Context(), m))

	got, err := p.Predict(t.Context(), []float64{1.0})
	require.NoError(t, err)
	require.Equal(t, []float32{0.25, 0.5, 0.75}, got.Embedding)
	require.Equal(t, []float64{0.25, 0.5, 0.75}, got.Raw)
}

func TestPredictor_Positive_ArgvExactProtocol(t *testing.T) {
	t.Parallel()
	recordFile := filepath.Join(t.TempDir(), "argv.txt")
	modelPath := writeModelFixture(t, runnerInstruction{
		Stdout:         "[1.0]",
		RecordArgvFile: recordFile,
	})

	p := newTestPredictor(onnx.Options{})
	m := tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}
	require.NoError(t, p.Load(t.Context(), m))
	_, err := p.Predict(t.Context(), []float64{1.0, 2.0})
	require.NoError(t, err)

	data, err := os.ReadFile(recordFile)
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Equal(t, []string{"--model", modelPath, "--inputs", "[1,2]"}, args)
}

func TestPredictor_Positive_InputNamePassthrough(t *testing.T) {
	t.Parallel()
	recordFile := filepath.Join(t.TempDir(), "argv.txt")
	modelPath := writeModelFixture(t, runnerInstruction{
		Stdout:         "[1.0]",
		RecordArgvFile: recordFile,
	})

	p := newTestPredictor(onnx.Options{InputName: "tensor_input"})
	m := tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}
	require.NoError(t, p.Load(t.Context(), m))
	_, err := p.Predict(t.Context(), []float64{3.0})
	require.NoError(t, err)

	data, err := os.ReadFile(recordFile)
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.True(t, slices.Contains(args, "--input-name"))
	idx := slices.Index(args, "--input-name")
	require.Equal(t, "tensor_input", args[idx+1])
}

func TestPredictor_Positive_FileURIAndLabelsOverride(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[0.8, 0.2]"})
	fileURI, err := fileuri.FromPath(modelPath)
	require.NoError(t, err)

	p := newTestPredictor(onnx.Options{Labels: []string{"opt_a", "opt_b"}})
	m := tiny.Model{URI: fileURI, TaskKind: tiny.TaskClassify}
	require.NoError(t, p.Load(t.Context(), m))

	got, err := p.Predict(t.Context(), []float64{1.0})
	require.NoError(t, err)
	require.Len(t, got.Labels, 2)
	require.Equal(t, "opt_a", got.Labels[0].Label)
	require.NoError(t, p.Close())
}

func TestPredictor_Negative_MissingRunner(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{})
	p := onnx.NewPredictor(onnx.Options{RunnerPath: "/nonexistent/vmafx-ort-runner"})
	err := p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress})
	require.Error(t, err)
	require.True(t, errors.Is(err, onnx.ErrRunnerUnavailable))
}

func TestPredictor_Negative_ExitCodeMappings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		exitCode int
		stderr   string
		wantErr  error
	}{
		{"exit 1 model failure", 1, "model could not execute", onnx.ErrModelExecution},
		{"exit 2 usage error", 2, "invalid flags", onnx.ErrRunnerUsage},
		{"exit 3 runner without ORT", 3, "built without ORT", onnx.ErrRunnerUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			modelPath := writeModelFixture(t, runnerInstruction{
				ExitCode: tc.exitCode,
				Stderr:   tc.stderr,
			})
			p := newTestPredictor(onnx.Options{})
			m := tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}
			require.NoError(t, p.Load(t.Context(), m))

			_, err := p.Predict(t.Context(), []float64{1.0})
			require.Error(t, err)
			require.True(t, errors.Is(err, tc.wantErr))
			require.Contains(t, err.Error(), tc.stderr)
		})
	}
}

func TestPredictor_Negative_MalformedStdout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		stdout      string
		emptyStdout bool
		want        string
	}{
		{"non-JSON", "not-json-array", false, "decode output"},
		{"trailing data", "[1.0] trailing", false, "unexpected trailing data"},
		{"empty output", "", true, "runner output empty"},
		{"null output", "null", false, "runner output is null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			modelPath := writeModelFixture(t, runnerInstruction{
				Stdout:      tc.stdout,
				EmptyStdout: tc.emptyStdout,
			})
			p := newTestPredictor(onnx.Options{})
			require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))
			_, err := p.Predict(t.Context(), []float64{1.0})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestPredictor_Negative_OutputLengths(t *testing.T) {
	t.Parallel()
	t.Run("regress wrong length", func(t *testing.T) {
		t.Parallel()
		modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[1.0, 2.0]"})
		p := newTestPredictor(onnx.Options{})
		require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))
		_, err := p.Predict(t.Context(), []float64{1.0})
		require.ErrorContains(t, err, "regress output length 2 != 1")
	})
	t.Run("classify wrong length", func(t *testing.T) {
		t.Parallel()
		modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[0.5]"})
		p := newTestPredictor(onnx.Options{})
		m := tiny.Model{URI: modelPath, TaskKind: tiny.TaskClassify, Labels: []string{"a", "b"}}
		require.NoError(t, p.Load(t.Context(), m))
		_, err := p.Predict(t.Context(), []float64{1.0})
		require.ErrorContains(t, err, "classify output length 1 != label count 2")
	})
}

func TestPredictor_Negative_NonFiniteAndOversized(t *testing.T) {
	t.Parallel()
	t.Run("non-finite 1e400", func(t *testing.T) {
		t.Parallel()
		modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[1e400]"})
		p := newTestPredictor(onnx.Options{})
		require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))
		_, err := p.Predict(t.Context(), []float64{1.0})
		require.Error(t, err)
	})
	t.Run("nan in stdout", func(t *testing.T) {
		t.Parallel()
		modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[NaN]"})
		p := newTestPredictor(onnx.Options{})
		require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))
		_, err := p.Predict(t.Context(), []float64{1.0})
		require.Error(t, err)
	})
	t.Run("oversized output slice", func(t *testing.T) {
		t.Parallel()
		builder := strings.Builder{}
		builder.WriteString("[")
		for range 65537 {
			builder.WriteString("1.0,")
		}
		builder.WriteString("1.0]")
		modelPath := writeModelFixture(t, runnerInstruction{Stdout: builder.String()})
		p := newTestPredictor(onnx.Options{})
		require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskEmbed}))
		_, err := p.Predict(t.Context(), []float64{1.0})
		require.ErrorContains(t, err, "exceeds maximum 65536")
	})
	t.Run("oversized stdout bytes", func(t *testing.T) {
		t.Parallel()
		giant := strings.Repeat("x", int(onnx.MaxStdoutBytes)+100)
		modelPath := writeModelFixture(t, runnerInstruction{Stdout: giant})
		p := newTestPredictor(onnx.Options{})
		require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))
		_, err := p.Predict(t.Context(), []float64{1.0})
		require.ErrorContains(t, err, "runner stdout exceeded")
	})
}

func TestPredictor_Negative_InputTypeAndState(t *testing.T) {
	t.Parallel()
	p := newTestPredictor(onnx.Options{})
	_, err := p.Predict(t.Context(), []float64{1.0})
	require.ErrorContains(t, err, "Load not called")

	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[1.0]"})
	require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))

	for _, invalid := range []any{"string", 42, nil, []int{1, 2}} {
		_, err := p.Predict(t.Context(), invalid)
		require.ErrorContains(t, err, "input must be []float64 or []float32")
	}
}

func TestPredictor_Negative_LoadValidation(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{})
	p := newTestPredictor(onnx.Options{})

	require.ErrorContains(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskGenerate}), "TaskKind=generate not supported")
	require.ErrorContains(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress, Format: tiny.FormatTFLite}), "Format=tflite not supported")
	require.ErrorContains(t, p.Load(t.Context(), tiny.Model{URI: "", TaskKind: tiny.TaskRegress}), "model.URI empty")
	require.ErrorContains(t, p.Load(t.Context(), tiny.Model{URI: "/nonexistent/model.onnx", TaskKind: tiny.TaskRegress}), "stat model")
	require.ErrorContains(t, p.Load(t.Context(), tiny.Model{URI: t.TempDir(), TaskKind: tiny.TaskRegress}), "not a regular file")
	require.ErrorContains(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskClassify, Labels: []string{"single"}}), "validate labels")
}

func TestPredictor_Boundary_InputLength(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[1.0]"})
	p := newTestPredictor(onnx.Options{})
	require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))

	okInput := make([]float64, onnx.MaxInputLength)
	_, err := p.Predict(t.Context(), okInput)
	require.NoError(t, err)

	oversized := make([]float64, onnx.MaxInputLength+1)
	_, err = p.Predict(t.Context(), oversized)
	require.ErrorContains(t, err, "input length 4097 exceeds maximum 4096")

	_, err = p.Predict(t.Context(), []float32{})
	require.ErrorContains(t, err, "input is empty")
}

func TestPredictor_Boundary_TimeoutKillsRunner(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{SleepMs: 5000})
	p := newTestPredictor(onnx.Options{Timeout: 50 * time.Millisecond})
	require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))

	start := time.Now()
	_, err := p.Predict(t.Context(), []float64{1.0})
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Contains(t, err.Error(), "runner canceled")
	require.Less(t, elapsed, 3*time.Second)
}

func TestPredictor_Boundary_DeadlineWinsOverTimeout(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{SleepMs: 5000})
	p := newTestPredictor(onnx.Options{Timeout: 10 * time.Second})
	require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Predict(ctx, []float64{1.0})
	elapsed := time.Since(start)

	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
	require.Less(t, elapsed, 3*time.Second)
}

func TestPredictor_Concurrency(t *testing.T) {
	t.Parallel()
	modelPath := writeModelFixture(t, runnerInstruction{Stdout: "[42.0]"})
	p := newTestPredictor(onnx.Options{})
	require.NoError(t, p.Load(t.Context(), tiny.Model{URI: modelPath, TaskKind: tiny.TaskRegress}))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := p.Predict(context.Background(), []float64{1.0, 2.0})
			require.NoError(t, err)
			require.InDelta(t, 42.0, got.Value, 1e-6)
		}()
	}
	wg.Wait()
}
