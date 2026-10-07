// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tiny

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeDockerContainerID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeDockerCLI struct {
	mu               sync.Mutex
	calls            [][]string
	environmentFiles []string
	cidFiles         []string
	runErr           error
	stopErr          error
	killErr          error
	removeErr        error
	waitForCancel    bool
	started          chan struct{}
}

func (f *fakeDockerCLI) invoke(
	ctx context.Context,
	_ string,
	args []string,
	_ []string,
	_, _ io.Writer,
) error {
	f.mu.Lock()
	f.calls = append(f.calls, slices.Clone(args))
	f.mu.Unlock()
	if len(args) == 0 {
		return errors.New("fake docker: empty command")
	}
	if args[0] == "run" {
		envFile := dockerOptionValue(args, "--env-file")
		cidFile := dockerOptionValue(args, "--cidfile")
		if cidFile == "" {
			return errors.New("fake docker: missing --cidfile")
		}
		if err := os.WriteFile(cidFile, []byte(fakeDockerContainerID), 0o600); err != nil {
			return err
		}
		f.mu.Lock()
		f.environmentFiles = append(f.environmentFiles, envFile)
		f.cidFiles = append(f.cidFiles, cidFile)
		f.mu.Unlock()
		if f.started != nil {
			close(f.started)
		}
		if f.waitForCancel {
			<-ctx.Done()
			return errors.New("fake docker: CLI killed")
		}
		return f.runErr
	}
	if len(args) < 2 || args[0] != "container" {
		return errors.New("fake docker: unexpected command")
	}
	switch args[1] {
	case "stop":
		return f.stopErr
	case "kill":
		return f.killErr
	case "rm":
		return f.removeErr
	default:
		return errors.New("fake docker: unexpected container command")
	}
}

func (f *fakeDockerCLI) snapshot() (calls [][]string, envFiles, cidFiles []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls = make([][]string, len(f.calls))
	for index := range f.calls {
		calls[index] = slices.Clone(f.calls[index])
	}
	return calls, slices.Clone(f.environmentFiles), slices.Clone(f.cidFiles)
}

func dockerOptionValue(args []string, option string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == option {
			return args[index+1]
		}
	}
	return ""
}

func TestBuildDockerArgsUsesSortedEnvironmentFile(t *testing.T) {
	t.Parallel()
	spec := RunSpec{
		Image:              "example.invalid/trainer@sha256:" + strings.Repeat("a", 64),
		InputDir:           "/input",
		OutputDir:          "/output",
		Env:                map[string]string{"Z_SECRET": "do-not-leak", "A_TOKEN": "also-secret"},
		AllowUnpinnedImage: false,
	}
	envFile, err := writeDockerEnvironmentFile(spec.Env)
	if err != nil {
		t.Fatalf("write Docker environment: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := removeDockerEnvironmentFile(envFile); cleanupErr != nil {
			t.Errorf("remove Docker environment: %v", cleanupErr)
		}
	})
	args := buildDockerArgs(
		&DockerRunner{},
		spec,
		envFile,
		"/tmp/container.cid",
		"container-name",
	)
	joined := strings.Join(args, "\x00")
	if strings.Contains(joined, "do-not-leak") || strings.Contains(joined, "also-secret") {
		t.Fatalf("docker argv exposes an environment value: %q", args)
	}
	if !strings.Contains(joined, "--env-file\x00"+envFile) {
		t.Fatalf("docker argv environment file = %q; want %q", args, envFile)
	}
	payload, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read Docker environment: %v", err)
	}
	want := "A_TOKEN=also-secret\nZ_SECRET=do-not-leak\n"
	if string(payload) != want {
		t.Fatalf("Docker environment file = %q; want %q", payload, want)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(envFile)
		if statErr != nil {
			t.Fatalf("stat Docker environment: %v", statErr)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("Docker environment mode = %o; want 600", got)
		}
	}
}

func TestValidateEnvironmentRejectsUnencodableValues(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]string{
		"line feed":     "first\nsecond",
		"carriage":      "first\rsecond",
		"nul":           "first\x00second",
		"invalid UTF-8": string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateEnvironment(map[string]string{"SECRET": value}); err == nil {
				t.Fatal("unencodable Docker environment value accepted")
			}
		})
	}
}

func TestValidateEnvironmentDockerLineBoundary(t *testing.T) {
	t.Parallel()
	exact := strings.Repeat("x", 65_533)
	if err := validateEnvironment(map[string]string{"A": exact}); err != nil {
		t.Fatalf("exact Docker env-file line boundary rejected: %v", err)
	}
	if err := validateEnvironment(map[string]string{"A": exact + "x"}); err == nil {
		t.Fatal("Docker env-file line overflow accepted")
	}
}

func TestDockerRunnerRemovesEnvironmentAndCIDFiles(t *testing.T) {
	t.Parallel()
	errRun := errors.New("fake run failure")
	for name, runErr := range map[string]error{"success": nil, "failure": errRun} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeDockerCLI{runErr: runErr}
			runner := &DockerRunner{command: newDockerCommandFunc(fake.invoke)}
			err := runner.Run(t.Context(), RunSpec{
				Image: "example.invalid/trainer:v1", Env: map[string]string{"TOKEN": "secret"},
				InputDir: t.TempDir(), OutputDir: t.TempDir(), AllowUnpinnedImage: true,
			})
			if runErr == nil && err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if runErr != nil && !errors.Is(err, runErr) {
				t.Fatalf("Run() error = %v; want %v", err, runErr)
			}
			calls, envFiles, cidFiles := fake.snapshot()
			if len(calls) < 3 || len(envFiles) != 1 || len(cidFiles) != 1 {
				t.Fatalf("Docker calls/env/cid = %q/%q/%q", calls, envFiles, cidFiles)
			}
			for _, path := range []string{envFiles[0], cidFiles[0], filepath.Dir(cidFiles[0])} {
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("temporary Docker path remains %q: %v", path, statErr)
				}
			}
			if strings.Contains(strings.Join(calls[0], "\x00"), "secret") {
				t.Fatalf("docker argv exposes environment value: %q", calls[0])
			}
		})
	}
}

func TestDockerRunnerCancellationStopsKillsAndRemovesContainer(t *testing.T) {
	t.Parallel()
	stopErr := errors.New("fake stop failure")
	fake := &fakeDockerCLI{
		stopErr: stopErr, waitForCancel: true, started: make(chan struct{}),
	}
	runner := &DockerRunner{command: newDockerCommandFunc(fake.invoke)}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	inputDir := t.TempDir()
	outputDir := t.TempDir()
	go func() {
		result <- runner.Run(ctx, RunSpec{
			Image: "example.invalid/trainer:v1", Env: map[string]string{"TOKEN": "secret"},
			InputDir: inputDir, OutputDir: outputDir, AllowUnpinnedImage: true,
		})
	}()
	<-fake.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v; want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Docker cancellation cleanup did not finish")
	}
	calls, _, _ := fake.snapshot()
	want := [][]string{
		{"run"},
		{"container", "stop"},
		{"container", "kill"},
		{"container", "rm"},
	}
	if len(calls) != len(want) {
		t.Fatalf("Docker calls = %q; want four lifecycle calls", calls)
	}
	for index := range want {
		if len(calls[index]) < len(want[index]) || !slices.Equal(calls[index][:len(want[index])], want[index]) {
			t.Fatalf("Docker call %d = %q; want prefix %q", index, calls[index], want[index])
		}
	}
}

func TestReadDockerContainerIDIsBounded(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "container.cid")
	if err := os.WriteFile(path, []byte(fakeDockerContainerID), 0o600); err != nil {
		t.Fatal(err)
	}
	containerID, err := readDockerContainerID(path)
	if err != nil || containerID != fakeDockerContainerID {
		t.Fatalf("readDockerContainerID() = %q, %v", containerID, err)
	}
	if err = os.WriteFile(path, []byte(fakeDockerContainerID+"a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = readDockerContainerID(path); err == nil {
		t.Fatal("oversized Docker container ID accepted")
	}
}

func TestBuildDockerArgsEnforcesLeastPrivilegeRuntime(t *testing.T) {
	t.Parallel()
	spec := RunSpec{
		Image:              "example.invalid/trainer@sha256:" + strings.Repeat("a", 64),
		InputDir:           "/input",
		OutputDir:          "/output",
		AllowUnpinnedImage: false,
	}
	args := buildDockerArgs(&DockerRunner{}, spec, "", "/tmp/container.cid", "container-name")
	wantPrefix := []string{
		"run",
		"--cidfile", "/tmp/container.cid",
		"--name", "container-name",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit", "256",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=1073741824,mode=1777",
	}
	if len(args) < len(wantPrefix) || !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("docker argv prefix = %q; want %q", args, wantPrefix)
	}
	if slices.Contains(args, "--rm") {
		t.Fatalf("docker argv delegates removal to --rm instead of explicit cleanup: %q", args)
	}
}

func TestBuildDockerArgsUsesDeclaredTmpfsBudget(t *testing.T) {
	t.Parallel()
	spec := RunSpec{
		Image:              "example.invalid/trainer:v1",
		InputDir:           "/input",
		OutputDir:          "/output",
		TmpfsBytes:         11 << 30,
		AllowUnpinnedImage: true,
	}
	args := buildDockerArgs(
		&DockerRunner{},
		spec,
		"",
		"/tmp/container.cid",
		"container-name",
	)
	want := "/tmp:rw,noexec,nosuid,size=11811160064,mode=1777"
	if !strings.Contains(strings.Join(args, "\x00"), "--tmpfs\x00"+want) {
		t.Fatalf("docker argv lacks declared tmpfs budget %q: %q", want, args)
	}
}

func TestBuildDockerArgsNetworkDenyOverridesRunnerConfiguration(t *testing.T) {
	t.Parallel()
	spec := RunSpec{
		Image:              "example.invalid/trainer@sha256:" + strings.Repeat("a", 64),
		InputDir:           "/input",
		OutputDir:          "/output",
		NetworkDisabled:    true,
		MaxOutputFileBytes: 4096,
		AllowUnpinnedImage: false,
	}
	args := buildDockerArgs(
		&DockerRunner{Network: "host"},
		spec,
		"",
		"/tmp/container.cid",
		"container-name",
	)
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "--network\x00none") {
		t.Fatalf("docker argv lacks network deny: %q", args)
	}
	if strings.Contains(joined, "--network\x00host") {
		t.Fatalf("docker argv preserved hostile network override: %q", args)
	}
	if !strings.Contains(joined, "--ulimit\x00fsize=4096:4096") {
		t.Fatalf("docker argv lacks output file cap: %q", args)
	}
}

func TestDockerCommandEnvironmentPreservesAndSortsHostValues(t *testing.T) {
	t.Setenv("TINY_REPLACED_SECRET", "old")
	t.Setenv("TINY_UNCHANGED", "kept")
	env := dockerCommandEnvironment()
	if !slices.IsSorted(env) {
		t.Fatalf("command environment is not sorted: %q", env)
	}
	want := map[string]string{
		"TINY_REPLACED_SECRET": "old",
		"TINY_UNCHANGED":       "kept",
	}
	seen := make(map[string]int, len(want))
	for _, assignment := range env {
		key, value, found := strings.Cut(assignment, "=")
		if expected, tracked := want[key]; tracked {
			seen[key]++
			if !found || value != expected {
				t.Fatalf("environment %q = %q, %t; want %q", key, value, found, expected)
			}
		}
	}
	for key := range want {
		if seen[key] != 1 {
			t.Fatalf("environment %q occurred %d times; want once", key, seen[key])
		}
	}
}

func TestDockerCommandEnvironmentDoesNotAllowContainerDockerHostOverride(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///trusted/docker.sock")
	envFile, err := writeDockerEnvironmentFile(map[string]string{
		"DOCKER_HOST": "tcp://attacker.invalid:2375",
		"HF_TOKEN":    "secret",
	})
	if err != nil {
		t.Fatalf("write Docker environment: %v", err)
	}
	t.Cleanup(func() {
		if err := removeDockerEnvironmentFile(envFile); err != nil {
			t.Errorf("remove Docker environment: %v", err)
		}
	})
	env := dockerCommandEnvironment()
	for _, assignment := range env {
		key, value, found := strings.Cut(assignment, "=")
		if found && key == "DOCKER_HOST" && value != "unix:///trusted/docker.sock" {
			t.Fatalf("container environment redirected Docker CLI to %q", value)
		}
	}
}

func TestWithRunTimeoutUsesFiniteDefault(t *testing.T) {
	t.Parallel()
	ctx, cancel := withRunTimeout(context.Background(), 0)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("zero timeout produced an unbounded context")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > DefaultRunTimeout {
		t.Fatalf("default deadline remaining = %v; want 0..%v", remaining, DefaultRunTimeout)
	}
}

func TestWithRunTimeoutPreservesSoonerCallerDeadline(t *testing.T) {
	t.Parallel()
	parent, cancelParent := context.WithTimeout(context.Background(), time.Minute)
	defer cancelParent()
	parentDeadline, _ := parent.Deadline()
	ctx, cancel := withRunTimeout(parent, DefaultRunTimeout)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(parentDeadline) {
		t.Fatalf("derived deadline = %v, %t; want caller deadline %v", deadline, ok, parentDeadline)
	}
}

func TestNormalizeRunTimeoutRejectsNegative(t *testing.T) {
	t.Parallel()
	if _, err := NormalizeRunTimeout(-time.Second); err == nil {
		t.Fatal("negative timeout accepted")
	}
}

func TestValidateRunSpecRejectsNegativeOutputLimit(t *testing.T) {
	t.Parallel()
	err := validateRunSpec(RunSpec{
		Image:              "example.invalid/trainer:v1",
		AllowUnpinnedImage: true,
		InputDir:           "/input",
		OutputDir:          "/output",
		MaxOutputFileBytes: -1,
	})
	if err == nil {
		t.Fatal("negative output file cap accepted")
	}
}

func TestValidateRunSpecRejectsNegativeTmpfsBudget(t *testing.T) {
	t.Parallel()
	err := validateRunSpec(RunSpec{
		Image:              "example.invalid/trainer:v1",
		AllowUnpinnedImage: true,
		InputDir:           "/input",
		OutputDir:          "/output",
		TmpfsBytes:         -1,
	})
	if err == nil {
		t.Fatal("negative tmpfs budget accepted")
	}
}

func TestValidateRunSpecRejectsNegativeGPUCount(t *testing.T) {
	t.Parallel()
	err := validateRunSpec(RunSpec{
		Image:              "example.invalid/trainer:v1",
		AllowUnpinnedImage: true,
		InputDir:           "/input",
		OutputDir:          "/output",
		GPUs:               -1,
	})
	if err == nil {
		t.Fatal("negative GPU count accepted")
	}
}

func TestValidateImageReferenceRequiresDigestOrExplicitTrust(t *testing.T) {
	t.Parallel()
	pinned := "example.invalid/trainer:v1@sha256:" + strings.Repeat("b", 64)
	if err := ValidateImageReference(pinned, false); err != nil {
		t.Fatalf("pinned image rejected: %v", err)
	}
	if err := ValidateImageReference("example.invalid/trainer:v1", false); err == nil {
		t.Fatal("mutable image accepted without explicit trust")
	}
	if err := ValidateImageReference("example.invalid/trainer:v1", true); err != nil {
		t.Fatalf("explicit unpinned image rejected: %v", err)
	}
	if err := ValidateImageReference("example.invalid/trainer@sha256:short", false); err == nil {
		t.Fatal("short digest accepted")
	}
}

func TestValidateRunSpecRejectsInvalidEnvironmentName(t *testing.T) {
	t.Parallel()
	err := validateRunSpec(RunSpec{
		Image:              "example.invalid/trainer:v1",
		AllowUnpinnedImage: true,
		InputDir:           "/input",
		OutputDir:          "/output",
		Env:                map[string]string{"BAD=NAME": "secret"},
	})
	if err == nil {
		t.Fatal("invalid environment name accepted")
	}
}
