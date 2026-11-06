//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
	"time"
)

var errSizingConfig = errors.New("invalid sizing config")

type sizingConfig struct {
	rps       int
	duration  time.Duration
	requests  int64
	memoryMiB float64
	cpuCores  float64
}

func loadSizingConfig(t *testing.T) sizingConfig {
	t.Helper()
	cfg, err := parseSizingConfig(os.Getenv)
	if err != nil {
		t.Fatalf("sizing: %v", err)
	}
	return cfg
}

// parseSizingConfig reads the SIZING_* variables through env so tests can feed fixed values.
func parseSizingConfig(env func(string) string) (sizingConfig, error) {
	rps, err := parseRPS(env("SIZING_RPS"))
	if err != nil {
		return sizingConfig{}, err
	}
	dur, requests, err := parseDuration(env("SIZING_DURATION"), rps)
	if err != nil {
		return sizingConfig{}, err
	}
	mem, err := parseBudget("SIZING_MEMORY_BUDGET_MIB", env("SIZING_MEMORY_BUDGET_MIB"), defaultMemoryBudgetMiB)
	if err != nil {
		return sizingConfig{}, err
	}
	cpu, err := parseBudget("SIZING_CPU_BUDGET", env("SIZING_CPU_BUDGET"), defaultCPUBudget)
	if err != nil {
		return sizingConfig{}, err
	}
	return sizingConfig{rps: rps, duration: dur, requests: requests, memoryMiB: mem, cpuCores: cpu}, nil
}

func configError(name, raw, want string) error {
	return fmt.Errorf("%w: %s=%q: want %s", errSizingConfig, name, raw, want)
}

func parseRPS(raw string) (int, error) {
	if raw == "" {
		return defaultRPS, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, configError("SIZING_RPS", raw, "an integer >= 1")
	}
	return n, nil
}

// parseDuration also returns the request count, round(seconds * rps), which must be at least 1.
func parseDuration(raw string, rps int) (time.Duration, int64, error) {
	d := defaultDuration
	if raw != "" {
		var err error
		if d, err = time.ParseDuration(raw); err != nil {
			return 0, 0, configError("SIZING_DURATION", raw, "a Go duration such as 30s")
		}
	}
	requests := math.Round(d.Seconds() * float64(rps))
	if !(requests >= 1 && requests <= maxRequests) {
		return 0, 0, configError("SIZING_DURATION", raw, fmt.Sprintf("a duration yielding 1..%d requests at %d rps", maxRequests, rps))
	}
	return d, int64(requests), nil
}

func parseBudget(name, raw string, def float64) (float64, error) {
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
		return 0, configError(name, raw, "a finite number > 0")
	}
	return n, nil
}
