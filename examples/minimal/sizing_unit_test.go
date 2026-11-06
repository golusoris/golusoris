//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestSizingConfigParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		env      map[string]string
		wantRPS  int
		wantReqs int64
	}{
		{name: "defaults", env: nil, wantRPS: defaultRPS, wantReqs: 6000},
		{name: "positive", env: map[string]string{"SIZING_RPS": "50", "SIZING_DURATION": "10s", "SIZING_CPU_BUDGET": "0.5", "SIZING_MEMORY_BUDGET_MIB": "256"}, wantRPS: 50, wantReqs: 500},
		{name: "boundary rps 1", env: map[string]string{"SIZING_RPS": "1", "SIZING_DURATION": "1s"}, wantRPS: 1, wantReqs: 1},
		{name: "rounds fractional requests", env: map[string]string{"SIZING_RPS": "3", "SIZING_DURATION": "500ms"}, wantRPS: 3, wantReqs: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseSizingConfig(envFrom(tc.env))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.rps != tc.wantRPS || cfg.requests != tc.wantReqs {
				t.Fatalf("rps/requests = %d/%d, want %d/%d", cfg.rps, cfg.requests, tc.wantRPS, tc.wantReqs)
			}
		})
	}
}

func TestSizingConfigReject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "non-integer rps", env: map[string]string{"SIZING_RPS": "2.5"}, wantErr: "SIZING_RPS"},
		{name: "rps 0", env: map[string]string{"SIZING_RPS": "0"}, wantErr: "SIZING_RPS"},
		{name: "rps negative", env: map[string]string{"SIZING_RPS": "-3"}, wantErr: "SIZING_RPS"},
		{name: "rps text", env: map[string]string{"SIZING_RPS": "fast"}, wantErr: "SIZING_RPS"},
		{name: "NaN cpu budget", env: map[string]string{"SIZING_CPU_BUDGET": "NaN"}, wantErr: "SIZING_CPU_BUDGET"},
		{name: "Inf cpu budget", env: map[string]string{"SIZING_CPU_BUDGET": "Inf"}, wantErr: "SIZING_CPU_BUDGET"},
		{name: "negative Inf memory budget", env: map[string]string{"SIZING_MEMORY_BUDGET_MIB": "-Inf"}, wantErr: "SIZING_MEMORY_BUDGET_MIB"},
		{name: "NaN memory budget", env: map[string]string{"SIZING_MEMORY_BUDGET_MIB": "nan"}, wantErr: "SIZING_MEMORY_BUDGET_MIB"},
		{name: "zero memory budget", env: map[string]string{"SIZING_MEMORY_BUDGET_MIB": "0"}, wantErr: "SIZING_MEMORY_BUDGET_MIB"},
		{name: "sub-request duration", env: map[string]string{"SIZING_RPS": "1", "SIZING_DURATION": "400ms"}, wantErr: "SIZING_DURATION"},
		{name: "zero duration", env: map[string]string{"SIZING_DURATION": "0s"}, wantErr: "SIZING_DURATION"},
		{name: "bad duration", env: map[string]string{"SIZING_DURATION": "soon"}, wantErr: "SIZING_DURATION"},
		{name: "too many requests", env: map[string]string{"SIZING_RPS": "1000000", "SIZING_DURATION": "10000h"}, wantErr: "SIZING_DURATION"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseSizingConfig(envFrom(tc.env))
			if !errors.Is(err, errSizingConfig) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %v naming %s", err, errSizingConfig, tc.wantErr)
			}
		})
	}
}

func TestParseVMHWM(t *testing.T) {
	t.Parallel()
	const status = "Name:\tminimal\nVmPeak:\t  900000 kB\nVmHWM:\t   12345 kB\nVmRSS:\t   10000 kB\n"
	got, err := parseVMHWM(status)
	if err != nil || got != 12345 {
		t.Fatalf("parseVMHWM = %d, %v; want 12345", got, err)
	}
	for name, bad := range map[string]string{"missing": "VmRSS:\t1 kB\n", "empty value": "VmHWM:\n", "non-numeric": "VmHWM:\tabc kB\n"} {
		if _, err := parseVMHWM(bad); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParseCPUTicks(t *testing.T) {
	t.Parallel()
	// Fields after comm: state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime.
	const stat = "42 (my app) name) S 1 42 42 0 -1 4194560 100 0 0 0 150 25 0 0 20 0 8 0 1000 0 0"
	got, err := parseCPUTicks(stat)
	if err != nil || got != 175 {
		t.Fatalf("parseCPUTicks = %d, %v; want 175", got, err)
	}
	for name, bad := range map[string]string{"no comm": "42 app S 1", "short": "42 (app) S 1 2", "bad utime": "42 (a) S 1 1 1 0 -1 0 0 0 0 0 x 1"} {
		if _, err := parseCPUTicks(bad); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestBudgetViolations(t *testing.T) {
	t.Parallel()
	cfg := sizingConfig{memoryMiB: 512, cpuCores: 0.25}
	tests := []struct {
		name      string
		rss, cpu  float64
		failed    int64
		wantCount int
	}{
		{name: "under", rss: 100, cpu: 0.1, wantCount: 0},
		{name: "equal passes", rss: 512, cpu: 0.25, wantCount: 0},
		{name: "memory over", rss: 512.5, cpu: 0.1, wantCount: 1},
		{name: "cpu over", rss: 100, cpu: 0.2501, wantCount: 1},
		{name: "errors", rss: 100, cpu: 0.1, failed: 2, wantCount: 1},
		{name: "all over", rss: 600, cpu: 1, failed: 1, wantCount: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := budgetViolations(cfg, tc.rss, tc.cpu, loadResult{sent: 10, failed: tc.failed})
			if len(got) != tc.wantCount {
				t.Fatalf("violations = %v, want %d", got, tc.wantCount)
			}
		})
	}
}

func TestBudgetAchievedRPS(t *testing.T) {
	t.Parallel()
	if got := achievedRPS(loadResult{completed: 90, wall: 2 * time.Second}); got != 45 {
		t.Fatalf("achievedRPS = %v, want 45", got)
	}
	if got := achievedRPS(loadResult{completed: 90}); got != 0 {
		t.Fatalf("achievedRPS with zero wall = %v, want 0", got)
	}
}

func TestBudgetAvgCores(t *testing.T) {
	t.Parallel()
	got := avgCores(procUsage{cpuSeconds: 1}, procUsage{cpuSeconds: 2.5}, 3*time.Second)
	if got != 0.5 {
		t.Fatalf("avgCores = %v, want 0.5", got)
	}
}

func TestBudgetErrSinkBounded(t *testing.T) {
	t.Parallel()
	s := newErrSink()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { s.add(errStatus) })
	}
	wg.Wait()
	n, first := s.snapshot()
	if n != 20 || len(first) != maxKeptErrors {
		t.Fatalf("count/kept = %d/%d, want 20/%d", n, len(first), maxKeptErrors)
	}
}

func TestParseLockedBuffer(t *testing.T) {
	t.Parallel()
	var b lockedBuffer
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = b.Write([]byte("x")) })
	}
	wg.Wait()
	if got := b.String(); got != strings.Repeat("x", 8) {
		t.Fatalf("buffer = %q", got)
	}
}

func TestParseRetryable(t *testing.T) {
	t.Parallel()
	if !retryable(errAppExited, "listen tcp: bind: address already in use") {
		t.Error("exit with address in use must retry")
	}
	if retryable(errAppExited, "panic: boom") {
		t.Error("exit for another reason must not retry")
	}
	if retryable(errors.New("timeout"), "address already in use") {
		t.Error("a live app must not retry")
	}
}
