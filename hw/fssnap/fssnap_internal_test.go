// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fssnap

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
	toolPath := filepath.Join(dir, "zfs")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\nprintf '%s\\n' 'fixture diagnostic' >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if _, err := output(context.Background(), toolZFS, "list"); err == nil ||
		!strings.Contains(err.Error(), "fixture diagnostic") {
		t.Fatalf("output() error = %v, want tool stderr", err)
	}
}
