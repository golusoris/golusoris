// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package onnx implements a [tiny.Predictor] that executes ONNX models via an external runner subprocess speaking the vmafx-ort-runner protocol.
//
// The runner owns ONNX Runtime, so this package needs no cgo. Each
// [Predictor.Predict] call executes one forward pass:
//
//	<runner> --model <path.onnx> --inputs '<JSON array of numbers>' [--input-name <name>]
//
// stdout receives one JSON array of numbers plus newline; diagnostics go to stderr.
package onnx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/internal/fileuri"
)

// DefaultRunnerPath is the default executable looked up on PATH.
const DefaultRunnerPath = "vmafx-ort-runner"

// DefaultTimeout bounds runner execution when the context has no deadline.
const DefaultTimeout = 10 * time.Second

// MaxInputLength is the maximum number of float elements accepted by Predict.
const MaxInputLength = 4096

// MaxOutputLength is the maximum number of float elements allowed in runner output.
const MaxOutputLength = 65536

// MaxStdoutBytes caps runner stdout reading.
const MaxStdoutBytes int64 = 1 << 20 // 1 MiB

// MaxStderrBytes caps runner stderr captured for error reporting.
const MaxStderrBytes int64 = 4 << 10 // 4 KiB

var (
	// ErrRunnerUnavailable indicates the runner was not found or was built without ORT support (exit code 3).
	ErrRunnerUnavailable = errors.New("ai/tiny/serve/onnx: runner unavailable")

	// ErrModelExecution indicates the model could not be opened or executed by the runner (exit code 1).
	ErrModelExecution = errors.New("ai/tiny/serve/onnx: model execution failed")

	// ErrRunnerUsage indicates a runner usage or protocol error (exit code 2).
	ErrRunnerUsage = errors.New("ai/tiny/serve/onnx: runner usage error")
)

// Options configures an ONNX [Predictor].
type Options struct {
	// RunnerPath is the path or executable name for the runner subprocess (default "vmafx-ort-runner").
	RunnerPath string
	// InputName, when non-empty, binds the input by graph input name instead of positionally.
	InputName string
	// Timeout bounds runner execution when the context has no deadline (default 10s).
	Timeout time.Duration
	// Labels defines the expected label set for TaskClassify. If empty, the loaded Model.Labels is used.
	Labels []string
}

// Predictor runs ONNX inference via an external runner subprocess.
// Safe for concurrent Predict calls after Load returns.
type Predictor struct {
	opts Options

	mu         sync.RWMutex
	loaded     bool
	modelPath  string
	taskKind   tiny.TaskKind
	labels     []string
	runnerPath string
}

// NewPredictor returns a Predictor with the given options.
func NewPredictor(opts Options) *Predictor {
	if opts.RunnerPath == "" {
		opts.RunnerPath = DefaultRunnerPath
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	return &Predictor{opts: opts}
}

// Load validates the model task and artifact, and verifies the runner executable exists.
func (p *Predictor) Load(ctx context.Context, m tiny.Model) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("ai/tiny/serve/onnx: load: %w", err)
	}
	switch m.TaskKind {
	case tiny.TaskRegress, tiny.TaskClassify, tiny.TaskEmbed:
	case tiny.TaskGenerate:
		return fmt.Errorf("ai/tiny/serve/onnx: TaskKind=%s not supported", m.TaskKind)
	default:
		return fmt.Errorf("ai/tiny/serve/onnx: TaskKind=%s not supported", m.TaskKind)
	}
	if m.Format != "" && m.Format != tiny.FormatONNX {
		return fmt.Errorf("ai/tiny/serve/onnx: Format=%s not supported (want onnx)", m.Format)
	}

	labels, err := p.resolveLabels(m)
	if err != nil {
		return err
	}

	modelPath, err := resolveModelPath(m.URI)
	if err != nil {
		return err
	}

	resolvedRunner, lookErr := exec.LookPath(p.runnerExecutable())
	if lookErr != nil {
		return fmt.Errorf("ai/tiny/serve/onnx: resolve runner: %w: %w", ErrRunnerUnavailable, lookErr)
	}

	p.mu.Lock()
	p.loaded = true
	p.modelPath = modelPath
	p.taskKind = m.TaskKind
	p.labels = labels
	p.runnerPath = resolvedRunner
	p.mu.Unlock()
	return nil
}

func (p *Predictor) runnerExecutable() string {
	if p.opts.RunnerPath != "" {
		return p.opts.RunnerPath
	}
	return DefaultRunnerPath
}

func (p *Predictor) resolveLabels(m tiny.Model) ([]string, error) {
	if m.TaskKind != tiny.TaskClassify {
		return nil, nil
	}
	labels := m.Labels
	if len(p.opts.Labels) > 0 {
		labels = p.opts.Labels
	}
	if err := tiny.ValidateClassifierLabels(labels); err != nil {
		return nil, fmt.Errorf("ai/tiny/serve/onnx: validate labels: %w", err)
	}
	return slices.Clone(labels), nil
}

func resolveModelPath(uri string) (string, error) {
	if uri == "" {
		return "", errors.New("ai/tiny/serve/onnx: model.URI empty")
	}
	path := uri
	if strings.HasPrefix(uri, "file:") {
		parsed, err := url.Parse(uri)
		if err != nil {
			return "", fmt.Errorf("ai/tiny/serve/onnx: parse model URI: %w", err)
		}
		path = fileuri.ToPath(parsed)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("ai/tiny/serve/onnx: stat model: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("ai/tiny/serve/onnx: model path %q is not a regular file", path)
	}
	return path, nil
}

// Predict executes the model through the external runner subprocess.
func (p *Predictor) Predict(ctx context.Context, input any) (tiny.Prediction, error) {
	p.mu.RLock()
	loaded := p.loaded
	modelPath := p.modelPath
	taskKind := p.taskKind
	labels := p.labels
	runnerPath := p.runnerPath
	p.mu.RUnlock()

	if !loaded {
		return tiny.Prediction{}, errors.New("ai/tiny/serve/onnx: Load not called")
	}

	inputJSON, err := marshalInput(input)
	if err != nil {
		return tiny.Prediction{}, err
	}

	subCtx, cancel := p.bindTimeout(ctx)
	defer cancel()

	stdoutBuf := &boundedBuffer{limit: MaxStdoutBytes}
	stderrBuf := &boundedBuffer{limit: MaxStderrBytes}
	args := buildArgs(modelPath, string(inputJSON), p.opts.InputName)

	// #nosec G204 -- runnerPath is validated via exec.LookPath, modelPath is checked as regular file, and inputs are JSON-marshalled float slices.
	cmd := exec.CommandContext(subCtx, runnerPath, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if runErr := cmd.Run(); runErr != nil {
		return tiny.Prediction{}, explainExitError(subCtx, runErr, stderrBuf.String())
	}

	if stdoutBuf.exceeded {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/onnx: runner stdout exceeded %d bytes limit", MaxStdoutBytes)
	}

	outputs, err := parseOutputs(stdoutBuf.Bytes())
	if err != nil {
		return tiny.Prediction{}, err
	}

	return p.mapPrediction(taskKind, labels, outputs)
}

func (p *Predictor) bindTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	timeout := p.opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func buildArgs(modelPath, inputJSON, inputName string) []string {
	args := []string{"--model", modelPath, "--inputs", inputJSON}
	if inputName != "" {
		args = append(args, "--input-name", inputName)
	}
	return args
}

func marshalInput(input any) ([]byte, error) {
	switch in := input.(type) {
	case []float64:
		return marshalFloats(in)
	case []float32:
		return marshalFloats(in)
	default:
		return nil, fmt.Errorf("ai/tiny/serve/onnx: input must be []float64 or []float32, got %T", input)
	}
}

func marshalFloats[T ~float32 | ~float64](in []T) ([]byte, error) {
	if len(in) > MaxInputLength {
		return nil, fmt.Errorf("ai/tiny/serve/onnx: input length %d exceeds maximum %d", len(in), MaxInputLength)
	}
	if len(in) == 0 {
		return nil, errors.New("ai/tiny/serve/onnx: input is empty")
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("ai/tiny/serve/onnx: marshal input: %w", err)
	}
	return data, nil
}

func explainExitError(ctx context.Context, runErr error, stderr string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("ai/tiny/serve/onnx: runner canceled: %w (run err: %w)", ctxErr, runErr)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		msg := strings.TrimSpace(stderr)
		switch exitErr.ExitCode() {
		case 1:
			return fmt.Errorf("ai/tiny/serve/onnx: %w: %s", ErrModelExecution, msg)
		case 2:
			return fmt.Errorf("ai/tiny/serve/onnx: %w: %s", ErrRunnerUsage, msg)
		case 3:
			return fmt.Errorf("ai/tiny/serve/onnx: %w: %s", ErrRunnerUnavailable, msg)
		default:
			return fmt.Errorf("ai/tiny/serve/onnx: runner exit %d: %w: %s", exitErr.ExitCode(), runErr, msg)
		}
	}
	if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, os.ErrNotExist) {
		return fmt.Errorf("ai/tiny/serve/onnx: %w: %w", ErrRunnerUnavailable, runErr)
	}
	return fmt.Errorf("ai/tiny/serve/onnx: runner failed: %w", runErr)
}

func parseOutputs(data []byte) ([]float64, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("ai/tiny/serve/onnx: runner output empty")
	}
	var out []float64
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("ai/tiny/serve/onnx: decode output: %w", err)
	}
	if out == nil {
		return nil, errors.New("ai/tiny/serve/onnx: runner output is null, want JSON array")
	}
	if dec.InputOffset() < int64(len(trimmed)) {
		return nil, errors.New("ai/tiny/serve/onnx: unexpected trailing data in runner output")
	}
	if len(out) > MaxOutputLength {
		return nil, fmt.Errorf("ai/tiny/serve/onnx: output length %d exceeds maximum %d", len(out), MaxOutputLength)
	}
	for i, v := range out {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("ai/tiny/serve/onnx: output element %d is non-finite (%v)", i, v)
		}
	}
	return out, nil
}

func (p *Predictor) mapPrediction(taskKind tiny.TaskKind, labels []string, out []float64) (tiny.Prediction, error) {
	switch taskKind {
	case tiny.TaskRegress:
		if len(out) != 1 {
			return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/onnx: regress output length %d != 1", len(out))
		}
		return tiny.Prediction{Value: out[0], Raw: out}, nil
	case tiny.TaskClassify:
		if len(out) != len(labels) {
			return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/onnx: classify output length %d != label count %d", len(out), len(labels))
		}
		return tiny.Prediction{Labels: sortClassifyScores(labels, out), Raw: out}, nil
	case tiny.TaskEmbed:
		emb := make([]float32, len(out))
		for i, v := range out {
			emb[i] = float32(v)
		}
		return tiny.Prediction{Embedding: emb, Raw: out}, nil
	case tiny.TaskGenerate:
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/onnx: unsupported task kind %s", taskKind)
	default:
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/onnx: unsupported task kind %s", taskKind)
	}
}

func sortClassifyScores(labels []string, out []float64) []tiny.LabelScore {
	scored := make([]tiny.LabelScore, len(labels))
	for i, l := range labels {
		scored[i] = tiny.LabelScore{Label: l, Score: float32(out[i])}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Label < scored[j].Label
	})
	return scored
}

// Close is a no-op; the external runner is invoked per-request as a subprocess.
func (*Predictor) Close() error { return nil }

type boundedBuffer struct {
	buf      bytes.Buffer
	limit    int64
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - int64(b.buf.Len())
	if remaining <= 0 {
		b.exceeded = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		b.exceeded = true
		b.buf.Write(p[:remaining])
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}

// compile-time assertion: Predictor satisfies tiny.Predictor.
var _ tiny.Predictor = (*Predictor)(nil)
