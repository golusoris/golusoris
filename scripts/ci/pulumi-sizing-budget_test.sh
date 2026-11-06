#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Proves pulumi-sizing-budget.sh passes on the repository and fails with rc 1 on mutated or invalid inputs.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-pulumi-sizing-budget.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT
check="$repo_root/scripts/ci/pulumi-sizing-budget.sh"
app_go="$repo_root/deploy/pulumi/app.go"
sizing_go="$repo_root/examples/minimal/sizing_test.go"

expect_failure() {
	local expected="$1"
	shift
	local output rc=0
	output="$("$@" 2>&1)" || rc=$?
	if [[ "$rc" -ne 1 ]]; then
		printf 'want rc 1, got %s: %s\n' "$rc" "$*" >&2
		return 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing diagnostic %q in: %s\n' "$expected" "$output" >&2
		return 1
	fi
}

bash "$check" >/dev/null

sed -E 's/(taskMemory[[:space:]]*=[[:space:]]*)"512"/\1"1024"/' "$app_go" >"$suite_root/app-mem.go"
expect_failure 'taskMemory 1024 MiB != sizing default 512 MiB' bash "$check" "$suite_root/app-mem.go" "$sizing_go"

sed -E 's/(taskCPU[[:space:]]*=[[:space:]]*)"256"/\1"512"/' "$app_go" >"$suite_root/app-cpu.go"
expect_failure 'taskCPU 512 (0.5 vCPU) != sizing default 0.25 vCPU' bash "$check" "$suite_root/app-cpu.go" "$sizing_go"

sed -E 's/(defaultMemoryBudgetMiB[[:space:]]*=[[:space:]]*)512/\1256/' "$sizing_go" >"$suite_root/sizing-mem.go"
expect_failure 'taskMemory 512 MiB != sizing default 256 MiB' bash "$check" "$app_go" "$suite_root/sizing-mem.go"

sed -E 's/(defaultCPUBudget[[:space:]]*=[[:space:]]*)0\.25/\10.5/' "$sizing_go" >"$suite_root/sizing-cpu.go"
expect_failure 'taskCPU 256 (0.25 vCPU) != sizing default 0.5 vCPU' bash "$check" "$app_go" "$suite_root/sizing-cpu.go"

: >"$suite_root/empty.go"
expect_failure 'want exactly one taskCPU constant' bash "$check" "$suite_root/empty.go" "$sizing_go"

expect_failure "$suite_root/missing.go: not a regular file" bash "$check" "$suite_root/missing.go" "$sizing_go"
expect_failure "$suite_root/missing.go: not a regular file" bash "$check" "$app_go" "$suite_root/missing.go"
expect_failure "$suite_root: not a regular file" bash "$check" "$suite_root" "$sizing_go"
expect_failure "$suite_root: not a regular file" bash "$check" "$app_go" "$suite_root"

printf 'pulumi-sizing-budget_test: ok\n'
