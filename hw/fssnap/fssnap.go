// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package fssnap provides helpers for creating and managing ZFS and Btrfs
// snapshots by wrapping the respective CLI tools.
//
// This is a separate go.mod sub-module because it is Linux-specific and has no
// meaning on other platforms.  It has no external Go dependencies (pure stdlib).
// Import directly: github.com/golusoris/golusoris/hw/fssnap
//
// # ZFS
//
//	ctx := context.Background()
//	err := fssnap.ZFS.Snapshot(ctx, "tank/data", "2025-01-01")
//	snaps, err := fssnap.ZFS.List(ctx, "tank/data")
//	err = fssnap.ZFS.Destroy(ctx, "tank/data@2025-01-01")
//
// # Btrfs
//
//	err := fssnap.Btrfs.Snapshot(ctx, "/mnt/data", "/mnt/snaps/2025-01-01")
//	err = fssnap.Btrfs.Delete(ctx, "/mnt/snaps/2025-01-01")
package fssnap

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
)

// ZFS provides ZFS snapshot operations.
var ZFS zfsOps

type zfsOps struct{}

// Snapshot creates a ZFS snapshot: dataset@tag.
func (zfsOps) Snapshot(ctx context.Context, dataset, tag string) error {
	if err := validateZFSDataset(dataset); err != nil {
		return err
	}
	if err := validateZFSTag(tag); err != nil {
		return err
	}
	name := dataset + "@" + tag
	return run(ctx, toolZFS, "snapshot", name)
}

// List returns snapshot names for dataset.
func (zfsOps) List(ctx context.Context, dataset string) ([]string, error) {
	if err := validateZFSDataset(dataset); err != nil {
		return nil, err
	}
	out, err := output(ctx, toolZFS, "list", "-H", "-t", "snapshot", "-o", "name", "-r", dataset)
	if err != nil {
		return nil, err
	}
	var snaps []string
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if line != "" {
			snaps = append(snaps, line)
		}
	}
	return snaps, nil
}

// Destroy removes a snapshot (format: dataset@tag).
func (zfsOps) Destroy(ctx context.Context, snapshot string) error {
	if err := validateZFSSnapshot(snapshot); err != nil {
		return err
	}
	return run(ctx, toolZFS, "destroy", snapshot)
}

// Rollback rolls back dataset to snapshot.
func (zfsOps) Rollback(ctx context.Context, snapshot string) error {
	if err := validateZFSSnapshot(snapshot); err != nil {
		return err
	}
	return run(ctx, toolZFS, "rollback", snapshot)
}

// Btrfs provides Btrfs snapshot operations.
var Btrfs btrfsOps

type btrfsOps struct{}

// Snapshot creates a read-only Btrfs snapshot of src at dst.
func (btrfsOps) Snapshot(ctx context.Context, src, dst string) error {
	if err := validateOperand("Btrfs source", src); err != nil {
		return err
	}
	if err := validateOperand("Btrfs destination", dst); err != nil {
		return err
	}
	return run(ctx, toolBtrfs, "subvolume", "snapshot", "-r", src, dst)
}

// Delete removes a Btrfs snapshot at path.
func (btrfsOps) Delete(ctx context.Context, path string) error {
	if err := validateOperand("Btrfs snapshot path", path); err != nil {
		return err
	}
	return run(ctx, toolBtrfs, "subvolume", "delete", path)
}

// List returns snapshot paths under subvolume.
func (btrfsOps) List(ctx context.Context, subvolume string) ([]string, error) {
	if err := validateOperand("Btrfs subvolume", subvolume); err != nil {
		return nil, err
	}
	out, err := output(ctx, toolBtrfs, btrfsListArgs(subvolume)...)
	if err != nil {
		return nil, err
	}
	return parseBtrfsList(out)
}

func btrfsListArgs(subvolume string) []string {
	return []string{"subvolume", "list", "-r", "-s", "-o", subvolume}
}

func parseBtrfsList(out string) ([]string, error) {
	var snaps []string
	lineNumber := 0
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		lineNumber++
		if line == "" {
			continue
		}
		// format: "ID N gen G top level T path <path>"
		metadata, path, found := strings.Cut(line, " path ")
		if !found || !strings.HasPrefix(metadata, "ID ") || path == "" {
			return nil, fmt.Errorf("fssnap: malformed Btrfs list output at line %d", lineNumber)
		}
		snaps = append(snaps, path)
	}
	return snaps, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type tool string

const (
	toolZFS   tool = "zfs"
	toolBtrfs tool = "btrfs"
)

var (
	errNilContext       = errors.New("context is nil")
	errZFSDatasetSyntax = errors.New("dataset contains a snapshot, bookmark, range, or list separator")
	errZFSTagSyntax     = errors.New("snapshot tag contains a dataset, snapshot, bookmark, range, or list separator")
	errZFSSnapshotName  = errors.New("snapshot must be one dataset@tag")
)

func command(ctx context.Context, name tool, args ...string) (*exec.Cmd, error) {
	if ctx == nil {
		return nil, fmt.Errorf("fssnap: command: %w", errNilContext)
	}
	switch name {
	case toolZFS:
		// #nosec G204 -- executable is a fixed literal; args are passed directly without a shell.
		return exec.CommandContext(ctx, "zfs", args...), nil
	case toolBtrfs:
		// #nosec G204 -- executable is a fixed literal; args are passed directly without a shell.
		return exec.CommandContext(ctx, "btrfs", args...), nil
	default:
		return nil, fmt.Errorf("fssnap: unsupported tool %q", name)
	}
}

func validateZFSDataset(dataset string) error {
	if err := validateOperand("ZFS dataset", dataset); err != nil {
		return err
	}
	if strings.ContainsAny(dataset, "@#,%") {
		return fmt.Errorf("fssnap: validate ZFS dataset: %w", errZFSDatasetSyntax)
	}
	return nil
}

func validateZFSTag(tag string) error {
	if err := validateOperand("ZFS snapshot tag", tag); err != nil {
		return err
	}
	if strings.ContainsAny(tag, "/@#,%") {
		return fmt.Errorf("fssnap: validate ZFS tag: %w", errZFSTagSyntax)
	}
	return nil
}

func validateZFSSnapshot(snapshot string) error {
	if err := validateOperand("ZFS snapshot", snapshot); err != nil {
		return err
	}
	dataset, tag, found := strings.Cut(snapshot, "@")
	if !found || dataset == "" || tag == "" || strings.Contains(tag, "@") {
		return fmt.Errorf("fssnap: validate ZFS snapshot: %w", errZFSSnapshotName)
	}
	if err := validateZFSDataset(dataset); err != nil {
		return err
	}
	return validateZFSTag(tag)
}

func validateOperand(label, value string) error {
	if value == "" {
		return fmt.Errorf("fssnap: %s is empty", label)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("fssnap: %s starts with an option prefix", label)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("fssnap: %s contains a control character", label)
	}
	return nil
}

func run(ctx context.Context, name tool, args ...string) error {
	cmd, err := command(ctx, name, args...)
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fssnap: %s %v: %w\n%s", name, args, err, out)
	}
	return nil
}

func output(ctx context.Context, name tool, args ...string) (string, error) {
	cmd, err := command(ctx, name, args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fssnap: %s %v: %w\n%s", name, args, err, out)
	}
	return string(out), nil
}
