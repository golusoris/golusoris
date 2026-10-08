#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-allocation-budget.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

expect_failure() {
	local expected="$1"
	shift
	local output
	if output="$("$@" 2>&1)"; then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing diagnostic %q in: %s\n' "$expected" "$output" >&2
		return 1
	fi
}

run_fixture() {
	local fixture="$1"
	local name="$2"
	local case_root="$suite_root/$name"
	mkdir -p "$case_root"
	install -m 0644 "$fixture" "$case_root/budget_test.go"
	printf '%s\n' 'module allocationfixture' 'go 1.27.0' >"$case_root/go.mod"
	(
		cd "$case_root"
		go test -run '^$' -bench '^BenchmarkAllocationBudgetProbe$' \
			-benchmem -benchtime=1000x -count=1 .
	) >"$case_root/benchmark.txt"
	printf '%s\n' 'BenchmarkAllocationBudgetProbe 8 1' >"$case_root/budgets.tsv"
}

fixture_root="$repo_root/.config/hiss/testdata/HISS-03/go"
run_fixture "$fixture_root/negative/within-budget_test.go" under
bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/under/benchmark.txt" \
	--budgets "$suite_root/under/budgets.tsv" >/dev/null

run_fixture "$fixture_root/negative/at-budget_test.go" boundary
bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/boundary/benchmark.txt" \
	--budgets "$suite_root/boundary/budgets.tsv" >/dev/null

run_fixture "$fixture_root/positive/over-budget_test.go" over
expect_failure 'exceeds byte budget' \
	bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/over/benchmark.txt" \
	--budgets "$suite_root/over/budgets.tsv"

printf '%s\n' \
	'BenchmarkAllocationBudgetProbe-32 1000 1 ns/op 8 B/op 2 allocs/op' \
	>"$suite_root/alloc-over.txt"
expect_failure 'exceeds allocation budget' \
	bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/alloc-over.txt" \
	--budgets "$suite_root/under/budgets.tsv"

printf '%s\n' 'PASS' >"$suite_root/missing.txt"
expect_failure 'missing benchmark result' \
	bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/missing.txt" \
	--budgets "$suite_root/under/budgets.tsv"

cat "$suite_root/under/benchmark.txt" "$suite_root/under/benchmark.txt" \
	>"$suite_root/duplicate.txt"
expect_failure 'duplicate benchmark result' \
	bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/duplicate.txt" \
	--budgets "$suite_root/under/budgets.tsv"

expected_budgets=(
	BenchmarkNewUUID
	BenchmarkNewKSUID
	BenchmarkHashPassword
	BenchmarkSealOpen
	BenchmarkTypedSetGet
)
if [[ "$(grep -Evc '^[[:space:]]*(#|$)' \
	"$repo_root/scripts/ci/allocation-budgets.tsv")" -ne "${#expected_budgets[@]}" ]]; then
	printf 'production allocation budget set drifted\n' >&2
	exit 1
fi
for benchmark in "${expected_budgets[@]}"; do
	if [[ "$(grep -Ec "^${benchmark} [0-9]+ [0-9]+$" \
		"$repo_root/scripts/ci/allocation-budgets.tsv")" -ne 1 ]]; then
		printf 'missing production allocation budget: %s\n' "$benchmark" >&2
		exit 1
	fi
done

printf 'Allocation budget positive, negative, boundary, and fail-closed tests passed\n'
