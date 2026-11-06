//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import "fmt"

// achievedRPS is completed requests divided by the wall time of the run.
func achievedRPS(res loadResult) float64 {
	if res.wall <= 0 {
		return 0
	}
	return float64(res.completed) / res.wall.Seconds()
}

// budgetViolations lists every exceeded budget; a measurement equal to its budget passes.
func budgetViolations(cfg sizingConfig, rssMiB, cores float64, res loadResult) []string {
	var out []string
	if rssMiB > cfg.memoryMiB {
		out = append(out, fmt.Sprintf("peak RSS %.1f MiB exceeds budget %.0f MiB", rssMiB, cfg.memoryMiB))
	}
	if cores > cfg.cpuCores {
		out = append(out, fmt.Sprintf("avg CPU %.3f vCPU exceeds budget %.3f vCPU", cores, cfg.cpuCores))
	}
	if res.failed != 0 {
		out = append(out, "error rate "+res.errSummary()+", want 0")
	}
	return out
}
