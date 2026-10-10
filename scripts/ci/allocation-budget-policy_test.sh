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
			-benchmem -benchtime=1000x -count=5 .
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

# repeat_line prints its argument once per gate run.
repeat_line() {
	printf '%s\n' "$1" "$1" "$1" "$1" "$1"
}

repeat_line 'BenchmarkAllocationBudgetProbe-32 1000 1 ns/op 8 B/op 2 allocs/op' \
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
expect_failure 'repeat-count mismatch' \
	bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/duplicate.txt" \
	--budgets "$suite_root/under/budgets.tsv"

grep '^BenchmarkAllocationBudgetProbe' "$suite_root/under/benchmark.txt" | head -n 4 \
	>"$suite_root/short.txt"
expect_failure 'repeat-count mismatch' \
	bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/short.txt" \
	--budgets "$suite_root/under/budgets.tsv"

# One run inflated by host load must not fail when the other runs meet the budget (#717).
{
	repeat_line 'BenchmarkAllocationBudgetProbe-32 1000 1 ns/op 8 B/op 1 allocs/op' | head -n 4
	printf '%s\n' 'BenchmarkAllocationBudgetProbe-32 1000 1 ns/op 64 B/op 3 allocs/op'
} >"$suite_root/noisy.txt"
noisy_output="$(bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/noisy.txt" \
	--budgets "$suite_root/under/budgets.tsv")"
if [[ "$noisy_output" != *"allocs/op runs: 1 1 1 1 3 min 1 budget 1 headroom 0"* ]]; then
	printf 'gate output lacks per-run values, minimum and headroom: %s\n' "$noisy_output" >&2
	exit 1
fi

# A deterministic extra allocation raises every run, so the minimum still fails.
mkdir -p "$suite_root/plus-one"
printf '%s\n' 'package allocationfixture' '' 'import "testing"' '' \
	'var plusOneSink, plusOneExtra []byte' '' \
	'func BenchmarkAllocationBudgetProbe(b *testing.B) {' \
	'	b.ReportAllocs()' '	for b.Loop() {' \
	'		plusOneSink = make([]byte, 8)' '		plusOneExtra = make([]byte, 8)' '	}' '}' \
	>"$suite_root/plus-one-probe_test.go"
run_fixture "$suite_root/plus-one-probe_test.go" plus-one
printf '%s\n' 'BenchmarkAllocationBudgetProbe 64 1' >"$suite_root/plus-one/budgets.tsv"
expect_failure 'exceeds allocation budget: 2 allocs/op > 1 allocs/op (minimum of 5 runs)' \
	bash "$repo_root/scripts/ci/allocation-budget.sh" \
	--check-output "$suite_root/plus-one/benchmark.txt" \
	--budgets "$suite_root/plus-one/budgets.tsv"

# The production gate cannot resolve one allocation in HashPassword (fractional allocs/op,
# floor 58 to 60 against a cap of 70). It must still catch a regression past that headroom:
# 16 allocations planted through a build overlay put every run above the cap.
planted="$suite_root/password_planted.go"
awk '
	{ print }
	/^func HashPassword\(plain string\) \(string, error\) \{$/ {
		print "\tfor i := range plantedSinks {"
		print "\t\tplantedSinks[i] = new([16]byte)"
		print "\t}"
		planted = 1
	}
	END {
		if (!planted) {
			exit 1
		}
		print ""
		print "var plantedSinks [16]*[16]byte"
	}
' "$repo_root/core/crypto/password.go" >"$planted"
printf '{"Replace": {"%s": "%s"}}\n' "$repo_root/core/crypto/password.go" "$planted" \
	>"$suite_root/overlay.json"
expect_failure 'BenchmarkHashPassword exceeds allocation budget' \
	env GOFLAGS="-overlay=$suite_root/overlay.json" \
	bash "$repo_root/scripts/ci/allocation-budget.sh"

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
