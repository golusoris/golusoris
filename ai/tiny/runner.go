package tiny

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"
)

// Runner executes a containerized training job. Trainer packages
// (ai/tiny/gemma, ai/tiny/litert) stage inputs/outputs on the host and
// delegate the actual container launch to a Runner.
type Runner interface {
	Name() string
	Run(ctx context.Context, spec RunSpec) error
}

// RunSpec is the container-agnostic description of a training run.
// Input and output directories are mounted under /work/input and
// /work/output respectively inside the container.
type RunSpec struct {
	Image     string            // oci image ref
	Env       map[string]string // passed as env vars
	InputDir  string            // host dir, mounted read-only at /work/input
	OutputDir string            // host dir, mounted rw at /work/output
	Timeout   time.Duration     // 0 = no timeout beyond ctx
	GPUs      int               // 0 = CPU only
	Logger    io.Writer         // captures container stdout+stderr (nil ⇒ discarded)
}

// DockerRunner invokes `docker run` via exec.CommandContext.
type DockerRunner struct {
	// DockerPath is the docker binary (defaults to "docker" on PATH).
	DockerPath string
	// Pull controls `--pull`: "always" | "missing" | "never" (Docker default: "missing").
	Pull string
	// Network is `--network` (empty ⇒ default bridge).
	Network string
	// UserNSRemap toggles `--userns=host`; leave false unless the
	// container image expects matching UIDs.
	UserNSRemap bool
}

// Name reports "docker".
func (*DockerRunner) Name() string { return "docker" }

// Run executes `docker run --rm [--gpus N] -e ... -v IN:/work/input:ro
// -v OUT:/work/output IMAGE`.
func (r *DockerRunner) Run(ctx context.Context, spec RunSpec) error {
	if spec.Image == "" {
		return errors.New("ai/tiny: RunSpec.Image required")
	}
	if spec.InputDir == "" || spec.OutputDir == "" {
		return errors.New("ai/tiny: RunSpec.InputDir/OutputDir required")
	}
	dockerPath := r.DockerPath
	if dockerPath == "" {
		dockerPath = "docker"
	}
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	args := []string{"run", "--rm"}
	if r.Pull != "" {
		args = append(args, "--pull", r.Pull)
	}
	if r.Network != "" {
		args = append(args, "--network", r.Network)
	}
	if r.UserNSRemap {
		args = append(args, "--userns=host")
	}
	if spec.GPUs > 0 {
		args = append(args, "--gpus", strconv.Itoa(spec.GPUs))
	}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args,
		"-v", spec.InputDir+":/work/input:ro",
		"-v", spec.OutputDir+":/work/output:rw",
		spec.Image,
	)
	// #nosec G204 -- dockerPath is a constructor-configured binary path
	// and args are composed from validated Options (Image / Pull / Network)
	// + host-controlled InputDir/OutputDir, not untrusted input.
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	cmd.Stdout = spec.Logger
	cmd.Stderr = spec.Logger
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ai/tiny: docker run: %w", err)
	}
	return nil
}

// StubRunner is a test-only Runner. It invokes Fn in place of an
// actual container launch — tests simulate trainers by writing
// artifacts into spec.OutputDir from Fn.
type StubRunner struct {
	Fn func(ctx context.Context, spec RunSpec) error
}

// Name reports "stub".
func (*StubRunner) Name() string { return "stub" }

// Run invokes Fn (or returns nil when Fn is nil).
func (s *StubRunner) Run(ctx context.Context, spec RunSpec) error {
	if s.Fn == nil {
		return nil
	}
	return s.Fn(ctx, spec)
}
