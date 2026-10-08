// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tiny

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	Timeout   time.Duration     // 0 uses DefaultRunTimeout; negative is invalid
	GPUs      int               // 0 = CPU only
	Logger    io.Writer         // captures container stdout+stderr (nil ⇒ discarded)
	// NetworkDisabled forces --network=none even when DockerRunner.Network
	// requests a less restrictive mode.
	NetworkDisabled bool
	// MaxOutputFileBytes applies an RLIMIT_FSIZE hard and soft cap inside the
	// container. Zero omits the limit.
	MaxOutputFileBytes int64
	// TmpfsBytes caps /tmp. Zero uses [DefaultTmpfsBytes].
	TmpfsBytes int64
	// AllowUnpinnedImage explicitly trusts a mutable image reference. Keep
	// false outside local development; digest-pinned sha256 refs are required.
	AllowUnpinnedImage bool
}

// DefaultRunTimeout bounds a containerized training run when no explicit
// timeout is supplied.
const DefaultRunTimeout = 2 * time.Hour

// DefaultTmpfsBytes caps container /tmp storage at 1 GiB.
const DefaultTmpfsBytes int64 = 1 << 30

const (
	dockerCleanupTimeout = 30 * time.Second
	dockerStopTimeout    = 5
	dockerContainerIDLen = 64
)

type dockerCommandFunc func(
	context.Context,
	string,
	[]string,
	[]string,
	io.Writer,
	io.Writer,
) error

func newDockerCommandFunc(command dockerCommandFunc) *dockerCommandFunc {
	if command == nil {
		return nil
	}
	return &command
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

	command *dockerCommandFunc
}

// Name reports "docker".
func (*DockerRunner) Name() string { return "docker" }

// Run executes a least-privilege `docker run` with read-only root storage,
// dropped capabilities, and only the declared work mounts writable.
func (r *DockerRunner) Run(ctx context.Context, spec RunSpec) error {
	if err := validateRunSpec(spec); err != nil {
		return err
	}
	dockerPath := r.DockerPath
	if dockerPath == "" {
		dockerPath = "docker"
	}
	runTimeout, err := NormalizeRunTimeout(spec.Timeout)
	if err != nil {
		return err
	}
	ctx, cancel := withRunTimeout(ctx, runTimeout)
	defer cancel()

	envFile, err := writeDockerEnvironmentFile(spec.Env)
	if err != nil {
		return err
	}
	identity, err := newDockerRunIdentity()
	if err != nil {
		return errors.Join(err, removeDockerEnvironmentFile(envFile))
	}

	runErr := r.invokeDocker(
		ctx,
		dockerPath,
		buildDockerArgs(r, spec, envFile, identity.cidFile, identity.containerName),
		dockerCommandEnvironment(),
		spec.Logger,
		spec.Logger,
	)
	containerID, cidErr := readDockerContainerID(identity.cidFile)
	containerTarget := identity.containerName
	if cidErr == nil {
		containerTarget = containerID
	}
	cleanupErr := r.cleanupDockerContainer(ctx, dockerPath, containerTarget)
	temporaryErr := errors.Join(
		removeDockerEnvironmentFile(envFile),
		removeDockerRunIdentity(identity),
	)
	if runErr != nil && errors.Is(cidErr, os.ErrNotExist) {
		cidErr = nil
	}
	result := errors.Join(runErr, ctx.Err(), cidErr, cleanupErr, temporaryErr)
	if result != nil {
		return fmt.Errorf("ai/tiny: docker run: %w", result)
	}
	return nil
}

func (r *DockerRunner) invokeDocker(
	ctx context.Context,
	dockerPath string,
	args []string,
	environment []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if r.command != nil {
		return (*r.command)(ctx, dockerPath, args, environment, stdout, stderr)
	}
	// #nosec G204 -- dockerPath is constructor-configured; args are assembled
	// from validated options and host-controlled work directories.
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	cmd.Env = environment
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("invoke Docker: %w", err)
	}
	return nil
}

func (r *DockerRunner) cleanupDockerContainer(
	ctx context.Context,
	dockerPath string,
	containerTarget string,
) error {
	stopErr := r.invokeDockerCleanup(
		ctx,
		dockerPath,
		"container", "stop", "--timeout", strconv.Itoa(dockerStopTimeout), containerTarget,
	)
	var killErr error
	if stopErr != nil {
		killErr = r.invokeDockerCleanup(ctx, dockerPath, "container", "kill", containerTarget)
	}
	removeErr := r.invokeDockerCleanup(
		ctx,
		dockerPath,
		"container", "rm", "--force", containerTarget,
	)
	if removeErr == nil {
		return nil
	}
	return fmt.Errorf(
		"clean Docker container %q: %w",
		containerTarget,
		errors.Join(stopErr, killErr, removeErr),
	)
}

func (r *DockerRunner) invokeDockerCleanup(
	ctx context.Context,
	dockerPath string,
	args ...string,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dockerCleanupTimeout)
	defer cancel()
	return r.invokeDocker(
		cleanupCtx,
		dockerPath,
		args,
		dockerCommandEnvironment(),
		io.Discard,
		io.Discard,
	)
}

// validateRunSpec checks the fields Run needs before building a docker
// invocation.
func validateRunSpec(spec RunSpec) error {
	if spec.Image == "" {
		return errors.New("ai/tiny: RunSpec.Image required")
	}
	if spec.InputDir == "" || spec.OutputDir == "" {
		return errors.New("ai/tiny: RunSpec.InputDir/OutputDir required")
	}
	if err := ValidateImageReference(spec.Image, spec.AllowUnpinnedImage); err != nil {
		return err
	}
	if err := validateEnvironment(spec.Env); err != nil {
		return err
	}
	if _, err := NormalizeRunTimeout(spec.Timeout); err != nil {
		return err
	}
	if spec.MaxOutputFileBytes < 0 {
		return errors.New("ai/tiny: RunSpec.MaxOutputFileBytes must not be negative")
	}
	if spec.TmpfsBytes < 0 {
		return errors.New("ai/tiny: RunSpec.TmpfsBytes must not be negative")
	}
	if spec.GPUs < 0 {
		return errors.New("ai/tiny: RunSpec.GPUs must not be negative")
	}
	return nil
}

// NormalizeRunTimeout selects [DefaultRunTimeout] for zero and rejects
// negative durations.
func NormalizeRunTimeout(timeout time.Duration) (time.Duration, error) {
	if timeout < 0 {
		return 0, errors.New("ai/tiny: RunSpec.Timeout must not be negative")
	}
	if timeout == 0 {
		return DefaultRunTimeout, nil
	}
	return timeout, nil
}

// ValidateImageReference requires an exact sha256 digest unless the caller
// explicitly opts into a mutable development reference.
func ValidateImageReference(image string, allowUnpinned bool) error {
	if image == "" || strings.TrimSpace(image) != image {
		return errors.New("ai/tiny: image reference must be non-empty without surrounding whitespace")
	}
	const marker = "@sha256:"
	markerAt := strings.LastIndex(image, marker)
	if markerAt < 1 {
		if allowUnpinned {
			return nil
		}
		return fmt.Errorf("ai/tiny: image %q must include @sha256:<64 lowercase hex>; set AllowUnpinnedImage only for explicit local trust", image)
	}
	digest := image[markerAt+len(marker):]
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || strings.ToLower(digest) != digest {
		return fmt.Errorf("ai/tiny: image %q has invalid sha256 digest", image)
	}
	return nil
}

func validateEnvironment(env map[string]string) error {
	for key, value := range env {
		if !validEnvironmentName(key) {
			return fmt.Errorf("ai/tiny: invalid environment name %q", key)
		}
		if strings.ContainsAny(value, "\x00\r\n") || !utf8.ValidString(value) {
			return fmt.Errorf("ai/tiny: environment %q cannot be encoded in a Docker env file", key)
		}
		if len(key)+len(value)+2 > bufio.MaxScanTokenSize {
			return fmt.Errorf("ai/tiny: environment %q exceeds Docker's env-file line bound", key)
		}
	}
	return nil
}

func validEnvironmentName(name string) bool {
	if name == "" || len(name) > 256 {
		return false
	}
	if !validEnvironmentNameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !validEnvironmentNameStart(name[i]) && (name[i] < '0' || name[i] > '9') {
			return false
		}
	}
	return true
}

func validEnvironmentNameStart(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

// withRunTimeout derives a child context bounded by timeout. Zero selects the
// finite default; context.WithTimeout preserves an earlier caller deadline.
func withRunTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout == 0 {
		timeout = DefaultRunTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// buildDockerArgs assembles the `docker run` argument list for spec per
// r's configured options.
func buildDockerArgs(
	r *DockerRunner,
	spec RunSpec,
	envFile string,
	cidFile string,
	containerName string,
) []string {
	tmpfsBytes := spec.TmpfsBytes
	if tmpfsBytes == 0 {
		tmpfsBytes = DefaultTmpfsBytes
	}
	args := []string{
		"run",
		"--cidfile", cidFile,
		"--name", containerName,
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit", "256",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=" + strconv.FormatInt(tmpfsBytes, 10) + ",mode=1777",
	}
	if r.Pull != "" {
		args = append(args, "--pull", r.Pull)
	}
	if spec.NetworkDisabled {
		args = append(args, "--network", "none")
	} else if r.Network != "" {
		args = append(args, "--network", r.Network)
	}
	if r.UserNSRemap {
		args = append(args, "--userns=host")
	}
	if spec.GPUs > 0 {
		args = append(args, "--gpus", strconv.Itoa(spec.GPUs))
	}
	if spec.MaxOutputFileBytes > 0 {
		limit := strconv.FormatInt(spec.MaxOutputFileBytes, 10)
		args = append(args, "--ulimit", "fsize="+limit+":"+limit)
	}
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	return append(
		args,
		"-v", spec.InputDir+":/work/input:ro",
		"-v", spec.OutputDir+":/work/output:rw",
		spec.Image,
	)
}

type dockerRunIdentity struct {
	directory     string
	cidFile       string
	containerName string
}

func newDockerRunIdentity() (dockerRunIdentity, error) {
	directory, err := os.MkdirTemp("", "golusoris-tiny-docker-run-*")
	if err != nil {
		return dockerRunIdentity{}, fmt.Errorf("ai/tiny: create Docker run directory: %w", err)
	}
	return dockerRunIdentity{
		directory:     directory,
		cidFile:       filepath.Join(directory, "container.cid"),
		containerName: filepath.Base(directory),
	}, nil
}

func readDockerContainerID(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- path is created by newDockerRunIdentity.
	if err != nil {
		return "", fmt.Errorf("read Docker container ID file: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, dockerContainerIDLen+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return "", fmt.Errorf("read Docker container ID file: %w", errors.Join(readErr, closeErr))
	}
	containerID := string(payload)
	decoded, decodeErr := hex.DecodeString(containerID)
	if len(payload) != dockerContainerIDLen || decodeErr != nil || len(decoded) != 32 ||
		strings.ToLower(containerID) != containerID {
		return "", errors.New("read Docker container ID file: expected 64 lowercase hexadecimal bytes")
	}
	return containerID, nil
}

func removeDockerRunIdentity(identity dockerRunIdentity) error {
	var cidErr error
	if err := os.Remove(identity.cidFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		cidErr = fmt.Errorf("remove Docker container ID file: %w", err)
	}
	directoryErr := os.Remove(identity.directory)
	if directoryErr != nil {
		directoryErr = fmt.Errorf("remove Docker run directory: %w", directoryErr)
	}
	return errors.Join(cidErr, directoryErr)
}

func dockerCommandEnvironment() []string {
	return sortedEnvironment(os.Environ())
}

// sortedEnvironment keeps the last value per key and orders assignments by key.
func sortedEnvironment(assignments []string) []string {
	merged := make(map[string]string, len(assignments))
	for _, assignment := range assignments {
		key, value, found := splitEnvironmentAssignment(assignment)
		if found {
			merged[key] = value
		}
	}
	out := make([]string, 0, len(merged))
	for _, key := range slices.Sorted(maps.Keys(merged)) {
		out = append(out, key+"="+merged[key])
	}
	return out
}

// splitEnvironmentAssignment keeps one leading '=' in the key, as os/exec does,
// so Windows per-drive entries such as "=C:=C:\work" stay distinct.
func splitEnvironmentAssignment(assignment string) (key, value string, found bool) {
	if rest, hidden := strings.CutPrefix(assignment, "="); hidden {
		key, value, found = strings.Cut(rest, "=")
		return "=" + key, value, found
	}
	return strings.Cut(assignment, "=")
}

func writeDockerEnvironmentFile(env map[string]string) (string, error) {
	if len(env) == 0 {
		return "", nil
	}
	file, err := os.CreateTemp("", "golusoris-tiny-docker-env-*")
	if err != nil {
		return "", fmt.Errorf("ai/tiny: create Docker env file: %w", err)
	}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if _, err = io.WriteString(file, key+"="+env[key]+"\n"); err != nil {
			return "", discardDockerEnvironmentFile(file, err)
		}
	}
	if err = file.Close(); err != nil {
		return "", errors.Join(err, removeDockerEnvironmentFile(file.Name()))
	}
	return file.Name(), nil
}

func discardDockerEnvironmentFile(file *os.File, cause error) error {
	return errors.Join(cause, file.Close(), removeDockerEnvironmentFile(file.Name()))
}

func removeDockerEnvironmentFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove Docker env file: %w", err)
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
