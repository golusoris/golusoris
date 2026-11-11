// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fssnap

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Copies of this test binary work as fake CLI tools because shell scripts have no Windows executable form.
const (
	fakeToolEnv   = "GOLUSORIS_FSSNAP_FAKE_TOOL"
	fakeArgsEnv   = "GOLUSORIS_FSSNAP_FAKE_ARGS"
	fakeStdoutEnv = "GOLUSORIS_FSSNAP_FAKE_STDOUT"
)

func TestMain(m *testing.M) {
	if code, handled := runFakeTool(); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func runFakeTool() (int, bool) {
	switch os.Getenv(fakeToolEnv) {
	case "1":
		if _, err := os.Stderr.WriteString("fixture diagnostic\n"); err != nil {
			return 3, true
		}
		return 7, true
	case "ok":
		args := strings.Join(os.Args[1:], "\n") + "\n"
		if err := os.WriteFile(os.Getenv(fakeArgsEnv), []byte(args), 0o600); err != nil {
			return 2, true
		}
		if _, err := os.Stdout.WriteString(os.Getenv(fakeStdoutEnv)); err != nil {
			return 2, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// installFakeTool copies this test binary into dir under the executable name PATH lookup resolves for name.
func installFakeTool(t *testing.T, dir string, name tool) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	file := string(name)
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), binary, 0o700); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func useFakeTool(t *testing.T, name tool, stdout string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	installFakeTool(t, dir, name)
	argsFile = filepath.Join(dir, "args.txt")
	t.Setenv("PATH", dir)
	t.Setenv(fakeToolEnv, "ok")
	t.Setenv(fakeArgsEnv, argsFile)
	t.Setenv(fakeStdoutEnv, stdout)
	return argsFile
}

func readFakeArgs(t *testing.T, argsFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read fake tool args: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestZFSSnapshotPositive(t *testing.T) {
	argsFile := useFakeTool(t, toolZFS, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := ZFS.Snapshot(ctx, "tank/data", "daily"); err != nil {
		t.Fatalf("ZFS.Snapshot() error = %v", err)
	}
	want := []string{"snapshot", "tank/data@daily"}
	if got := readFakeArgs(t, argsFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("ZFS.Snapshot() argv = %q, want %q", got, want)
	}
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestZFSDestroyPositive(t *testing.T) {
	argsFile := useFakeTool(t, toolZFS, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := ZFS.Destroy(ctx, "tank/data@daily"); err != nil {
		t.Fatalf("ZFS.Destroy() error = %v", err)
	}
	want := []string{"destroy", "tank/data@daily"}
	if got := readFakeArgs(t, argsFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("ZFS.Destroy() argv = %q, want %q", got, want)
	}
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestZFSRollbackPositive(t *testing.T) {
	argsFile := useFakeTool(t, toolZFS, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := ZFS.Rollback(ctx, "tank/data@daily"); err != nil {
		t.Fatalf("ZFS.Rollback() error = %v", err)
	}
	want := []string{"rollback", "tank/data@daily"}
	if got := readFakeArgs(t, argsFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("ZFS.Rollback() argv = %q, want %q", got, want)
	}
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestZFSListPositiveAndBoundary(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   []string
	}{
		{name: "two snapshots", stdout: "tank/data@a\ntank/data@b\n", want: []string{"tank/data@a", "tank/data@b"}},
		{name: "empty"},
		{name: "blank middle line", stdout: "tank/data@a\n\ntank/data@b\n", want: []string{"tank/data@a", "tank/data@b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsFile := useFakeTool(t, toolZFS, tt.stdout)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			got, err := ZFS.List(ctx, "tank/data")
			if err != nil {
				t.Fatalf("ZFS.List() error = %v", err)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("ZFS.List() = %q, want %q", got, tt.want)
			}
			wantArgs := []string{"list", "-H", "-t", "snapshot", "-o", "name", "-r", "tank/data"}
			if gotArgs := readFakeArgs(t, argsFile); !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("ZFS.List() argv = %q, want %q", gotArgs, wantArgs)
			}
		})
	}
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestBtrfsSnapshotPositive(t *testing.T) {
	argsFile := useFakeTool(t, toolBtrfs, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := Btrfs.Snapshot(ctx, "/mnt/data", "/mnt/snaps/d1"); err != nil {
		t.Fatalf("Btrfs.Snapshot() error = %v", err)
	}
	want := []string{"subvolume", "snapshot", "-r", "/mnt/data", "/mnt/snaps/d1"}
	if got := readFakeArgs(t, argsFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("Btrfs.Snapshot() argv = %q, want %q", got, want)
	}
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestBtrfsDeletePositive(t *testing.T) {
	argsFile := useFakeTool(t, toolBtrfs, "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := Btrfs.Delete(ctx, "/mnt/snaps/d1"); err != nil {
		t.Fatalf("Btrfs.Delete() error = %v", err)
	}
	want := []string{"subvolume", "delete", "/mnt/snaps/d1"}
	if got := readFakeArgs(t, argsFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("Btrfs.Delete() argv = %q, want %q", got, want)
	}
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestBtrfsListPositiveAndBoundary(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   []string
	}{
		{name: "one snapshot", stdout: "ID 256 gen 42 top level 5 path snaps/d1\n", want: []string{"snaps/d1"}},
		{name: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsFile := useFakeTool(t, toolBtrfs, tt.stdout)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			got, err := Btrfs.List(ctx, "/mnt/data")
			if err != nil {
				t.Fatalf("Btrfs.List() error = %v", err)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("Btrfs.List() = %q, want %q", got, tt.want)
			}
			wantArgs := []string{"subvolume", "list", "-r", "-s", "-o", "/mnt/data"}
			if gotArgs := readFakeArgs(t, argsFile); !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("Btrfs.List() argv = %q, want %q", gotArgs, wantArgs)
			}
		})
	}
}

func TestZFSSnapshotIncludesToolStderr(t *testing.T) {
	useFakeTool(t, toolZFS, "")
	t.Setenv(fakeToolEnv, "1")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := ZFS.Snapshot(ctx, "tank/data", "daily"); err == nil ||
		!strings.Contains(err.Error(), "fixture diagnostic") {
		t.Fatalf("ZFS.Snapshot() error = %v, want tool stderr", err)
	}
}

//nolint:paralleltest // useFakeTool calls t.Setenv (PATH and fake-tool env), which forbids t.Parallel.
func TestBtrfsListRejectsMalformedToolOutput(t *testing.T) {
	useFakeTool(t, toolBtrfs, "garbage\n")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if got, err := Btrfs.List(ctx, "/mnt/data"); err == nil {
		t.Fatalf("Btrfs.List() = %q, nil error for malformed output", got)
	}
}

func TestZFSListToolFailure(t *testing.T) {
	useFakeTool(t, toolZFS, "")
	t.Setenv(fakeToolEnv, "1")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	got, err := ZFS.List(ctx, "tank/data")
	if err == nil || !strings.Contains(err.Error(), "fixture diagnostic") {
		t.Fatalf("ZFS.List() error = %v, want tool stderr", err)
	}
	if got != nil {
		t.Fatalf("ZFS.List() = %q, want nil on tool failure", got)
	}
}

func TestCommandRejectsExecutableOutsideAllowlist(t *testing.T) {
	t.Parallel()
	if _, err := command(context.Background(), tool("sh"), "-c", "true"); err == nil {
		t.Fatal("command() = nil error for executable outside allowlist")
	}
}

func TestCommandUsesFixedExecutable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name tool
		want string
	}{
		{name: toolZFS, want: "zfs"},
		{name: toolBtrfs, want: "btrfs"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			const opaqueArg = "pool/data;touch /tmp/not-executed"
			cmd, err := command(context.Background(), tt.name, "snapshot", opaqueArg)
			if err != nil {
				t.Fatalf("command(): %v", err)
			}
			if got := filepath.Base(cmd.Path); got != tt.want {
				t.Fatalf("command path = %q, want %q", got, tt.want)
			}
			if len(cmd.Args) != 3 || cmd.Args[2] != opaqueArg {
				t.Fatalf("command args = %q, want opaque argument %q", cmd.Args, opaqueArg)
			}
		})
	}
}

func TestCommandRejectsNilContext(t *testing.T) {
	t.Parallel()
	if _, err := command(nil, toolZFS, "list"); err == nil { //nolint:staticcheck // Deliberately test the nil guard.
		t.Fatal("command() = nil error for nil context")
	}
}

func TestSnapshotOperationsRejectOptionOperands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "ZFS snapshot dataset", run: func() error { return ZFS.Snapshot(ctx, "-r", "daily") }},
		{name: "ZFS snapshot tag", run: func() error { return ZFS.Snapshot(ctx, "tank/data", "-r") }},
		{name: "ZFS destroy", run: func() error { return ZFS.Destroy(ctx, "-r") }},
		{name: "ZFS rollback", run: func() error { return ZFS.Rollback(ctx, "-R") }},
		{name: "Btrfs source", run: func() error { return Btrfs.Snapshot(ctx, "-r", "/snap") }},
		{name: "Btrfs destination", run: func() error { return Btrfs.Snapshot(ctx, "/data", "-r") }},
		{name: "Btrfs delete", run: func() error { return Btrfs.Delete(ctx, "--recursive") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.run(); err == nil || !strings.Contains(err.Error(), "option prefix") {
				t.Fatalf("operation error = %v, want option-prefix rejection", err)
			}
		})
	}
}

func TestZFSOperationsRejectDatasetAndMultiSnapshotTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, target := range []string{
		"tank/data",
		"tank/data@",
		"@daily",
		"tank/data@daily@extra",
		"tank/data@daily,weekly",
		"tank/data@daily%weekly",
		"tank/data#bookmark",
	} {
		if err := ZFS.Destroy(ctx, target); err == nil {
			t.Errorf("ZFS.Destroy(%q) = nil error", target)
		}
		if err := ZFS.Rollback(ctx, target); err == nil {
			t.Errorf("ZFS.Rollback(%q) = nil error", target)
		}
	}
	if err := ZFS.Snapshot(ctx, "tank/data@old", "daily"); err == nil {
		t.Fatal("ZFS.Snapshot() accepted snapshot as dataset")
	}
	if err := ZFS.Snapshot(ctx, "tank/data", "daily,weekly"); err == nil {
		t.Fatal("ZFS.Snapshot() accepted snapshot list as tag")
	}
}

func TestListOperationsRejectOptionOperands(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := ZFS.List(ctx, "-r"); err == nil {
		t.Fatal("ZFS.List() = nil error for option operand")
	}
	if _, err := Btrfs.List(ctx, "--sort=path"); err == nil {
		t.Fatal("Btrfs.List() = nil error for option operand")
	}
}

func TestValidateOperandRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "-r", "path\nname", "path\x00name"} {
		if err := validateOperand("test operand", value); err == nil {
			t.Fatalf("validateOperand(%q) = nil error", value)
		}
	}
	if err := validateOperand("test operand", "snapshots/daily copy"); err != nil {
		t.Fatalf("validateOperand(valid) = %v", err)
	}
}

func TestParseBtrfsListPreservesPaths(t *testing.T) {
	t.Parallel()
	out := "ID 256 gen 42 top level 5 path snapshots/nightly copy\n" +
		"ID 257 gen 43 top level 5 uuid abc path snapshots/path marker path suffix  \n"
	want := []string{"snapshots/nightly copy", "snapshots/path marker path suffix  "}
	got, err := parseBtrfsList(out)
	if err != nil {
		t.Fatalf("parseBtrfsList() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBtrfsList() = %#v, want %#v", got, want)
	}
}

func TestParseBtrfsListRejectsMalformedOutput(t *testing.T) {
	t.Parallel()
	for _, out := range []string{"ID 256 gen 42 top level 5", "path snapshots/daily", "ID 256 path "} {
		if _, err := parseBtrfsList(out); err == nil {
			t.Fatalf("parseBtrfsList(%q) = nil error", out)
		}
	}
}

func TestBtrfsListArgsFilterBelowPath(t *testing.T) {
	t.Parallel()
	want := []string{"subvolume", "list", "-r", "-s", "-o", "/mnt/data"}
	if got := btrfsListArgs("/mnt/data"); !reflect.DeepEqual(got, want) {
		t.Fatalf("btrfsListArgs() = %q, want %q", got, want)
	}
}

func TestOutputIncludesToolStderr(t *testing.T) {
	dir := t.TempDir()
	installFakeTool(t, dir, toolZFS)
	t.Setenv(fakeToolEnv, "1")
	t.Setenv("PATH", dir)

	if _, err := output(context.Background(), toolZFS, "list"); err == nil ||
		!strings.Contains(err.Error(), "fixture diagnostic") {
		t.Fatalf("output() error = %v, want tool stderr", err)
	}
}
