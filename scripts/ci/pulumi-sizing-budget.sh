#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Fails when the sizing-test budget defaults in examples/minimal/sizing_test.go
# differ from the Fargate task size in deploy/pulumi/app.go (taskCPU units of
# 1/1024 vCPU, taskMemory in MiB). Usage: pulumi-sizing-budget.sh [app.go sizing_test.go]

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
app_go="${1:-$repo_root/deploy/pulumi/app.go}"
sizing_go="${2:-$repo_root/examples/minimal/sizing_test.go}"

# const_value prints the single value of `name = value` in a Go file, quotes stripped.
const_value() {
	local file="$1" name="$2" out
	out="$(sed -nE "s/^[[:space:]]*${name}[[:space:]]*=[[:space:]]*\"?([0-9.]+)\"?[[:space:]]*(\/\/.*)?\$/\\1/p" "$file")"
	if [[ -z "$out" || "$out" == *$'\n'* ]]; then
		printf 'pulumi-sizing-budget: %s: want exactly one %s constant\n' "$file" "$name" >&2
		return 1
	fi
	printf '%s\n' "$out"
}

task_cpu="$(const_value "$app_go" taskCPU)"
task_mem="$(const_value "$app_go" taskMemory)"
test_mem="$(const_value "$sizing_go" defaultMemoryBudgetMiB)"
test_cpu="$(const_value "$sizing_go" defaultCPUBudget)"

# awk compares decimals exactly enough: 256/1024 == 0.25.
if ! awk -v units="$task_cpu" -v want="$test_cpu" 'BEGIN { exit !(units / 1024 == want + 0) }'; then
	printf 'pulumi-sizing-budget: taskCPU %s (%s vCPU) != sizing default %s vCPU\n' \
		"$task_cpu" "$(awk -v u="$task_cpu" 'BEGIN { print u / 1024 }')" "$test_cpu" >&2
	exit 1
fi
if [[ "$task_mem" != "$test_mem" ]]; then
	printf 'pulumi-sizing-budget: taskMemory %s MiB != sizing default %s MiB\n' "$task_mem" "$test_mem" >&2
	exit 1
fi
printf 'pulumi-sizing-budget: ok (%s vCPU, %s MiB)\n' "$test_cpu" "$test_mem"
