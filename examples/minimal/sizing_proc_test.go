//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type procUsage struct {
	peakRSSKiB int64
	cpuSeconds float64
}

// readProc samples peak RSS (VmHWM) and utime+stime of pid.
func readProc(t *testing.T, pid int) procUsage {
	t.Helper()
	root := filepath.Join("/proc", strconv.Itoa(pid))
	status, err := os.ReadFile(filepath.Join(root, "status"))
	if err != nil {
		t.Fatalf("sizing: read status: %v", err)
	}
	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		t.Fatalf("sizing: read stat: %v", err)
	}
	hwm, err := parseVMHWM(string(status))
	if err != nil {
		t.Fatalf("sizing: %v", err)
	}
	ticks, err := parseCPUTicks(string(stat))
	if err != nil {
		t.Fatalf("sizing: %v", err)
	}
	return procUsage{peakRSSKiB: hwm, cpuSeconds: float64(ticks) / clockTicks}
}

// avgCores is the CPU seconds spent between two samples divided by the wall time between them.
func avgCores(before, after procUsage, wall time.Duration) float64 {
	if wall <= 0 {
		return 0
	}
	return (after.cpuSeconds - before.cpuSeconds) / wall.Seconds()
}

func parseVMHWM(status string) (int64, error) {
	for line := range strings.SplitSeq(status, "\n") {
		rest, ok := strings.CutPrefix(line, "VmHWM:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			break
		}
		kib, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmHWM %q: %w", rest, err)
		}
		return kib, nil
	}
	return 0, errors.New("VmHWM not found in /proc status")
}

// parseCPUTicks returns utime+stime (fields 14 and 15) of a /proc/<pid>/stat line.
func parseCPUTicks(stat string) (int64, error) {
	// Field 2 (comm) may contain spaces, so parsing resumes after its last closing parenthesis.
	idx := strings.LastIndexByte(stat, ')')
	if idx < 0 {
		return 0, errors.New("malformed /proc stat: no comm terminator")
	}
	fields := strings.Fields(stat[idx+1:])
	const utimeIdx, stimeIdx = 11, 12 // state is field 3 = index 0
	if len(fields) <= stimeIdx {
		return 0, fmt.Errorf("malformed /proc stat: %d fields after comm", len(fields))
	}
	utime, err := strconv.ParseInt(fields[utimeIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse utime: %w", err)
	}
	stime, err := strconv.ParseInt(fields[stimeIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse stime: %w", err)
	}
	return utime + stime, nil
}
