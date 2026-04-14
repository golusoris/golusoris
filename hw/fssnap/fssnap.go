// Package fssnap provides helpers for creating and managing ZFS and Btrfs
// snapshots by wrapping the respective CLI tools.
//
// This is a separate go.mod sub-module because it is Linux-specific and has no
// meaning on other platforms.  It has no external Go dependencies (pure stdlib).
// Import directly: github.com/golusoris/golusoris/hw/fssnap
//
// # ZFS
//
//	err := fssnap.ZFS.Snapshot("tank/data", "2025-01-01")
//	snaps, err := fssnap.ZFS.List("tank/data")
//	err  = fssnap.ZFS.Destroy("tank/data@2025-01-01")
//
// # Btrfs
//
//	err := fssnap.Btrfs.Snapshot("/mnt/data", "/mnt/snaps/2025-01-01")
//	err  = fssnap.Btrfs.Delete("/mnt/snaps/2025-01-01")
package fssnap

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ZFS provides ZFS snapshot operations.
var ZFS zfsOps

type zfsOps struct{}

// Snapshot creates a ZFS snapshot: dataset@tag.
func (zfsOps) Snapshot(ctx context.Context, dataset, tag string) error {
	name := dataset + "@" + tag
	return run(ctx, "zfs", "snapshot", name)
}

// List returns snapshot names for dataset.
func (zfsOps) List(ctx context.Context, dataset string) ([]string, error) {
	out, err := output(ctx, "zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-r", dataset)
	if err != nil {
		return nil, err
	}
	var snaps []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			snaps = append(snaps, line)
		}
	}
	return snaps, nil
}

// Destroy removes a snapshot (format: dataset@tag).
func (zfsOps) Destroy(ctx context.Context, snapshot string) error {
	return run(ctx, "zfs", "destroy", snapshot)
}

// Rollback rolls back dataset to snapshot.
func (zfsOps) Rollback(ctx context.Context, snapshot string) error {
	return run(ctx, "zfs", "rollback", snapshot)
}

// Btrfs provides Btrfs snapshot operations.
var Btrfs btrfsOps

type btrfsOps struct{}

// Snapshot creates a read-only Btrfs snapshot of src at dst.
func (btrfsOps) Snapshot(ctx context.Context, src, dst string) error {
	return run(ctx, "btrfs", "subvolume", "snapshot", "-r", src, dst)
}

// Delete removes a Btrfs snapshot at path.
func (btrfsOps) Delete(ctx context.Context, path string) error {
	return run(ctx, "btrfs", "subvolume", "delete", path)
}

// List returns snapshot paths under subvolume.
func (btrfsOps) List(ctx context.Context, subvolume string) ([]string, error) {
	out, err := output(ctx, "btrfs", "subvolume", "list", "-rs", subvolume)
	if err != nil {
		return nil, err
	}
	var snaps []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// format: "ID N gen G top level T path <path>"
		parts := strings.Fields(line)
		if len(parts) > 0 {
			snaps = append(snaps, parts[len(parts)-1])
		}
	}
	return snaps, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fssnap: %s %v: %w\n%s", name, args, err, out)
	}
	return nil
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("fssnap: %s %v: %w", name, args, err)
	}
	return string(out), nil
}
