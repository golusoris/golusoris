#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

allocation_suite_root=""

usage() {
	printf 'usage: %s [--root PATH] | --check-output FILE --budgets FILE\n' \
		"${0##*/}" >&2
}

# budget_manifest_rules prints the awk rules that load the budget manifest, the first input.
budget_manifest_rules() {
	cat <<'AWK'
		FNR == NR {
			if ($0 ~ /^[[:space:]]*(#|$)/) {
				next
			}
			if (NF != 3 || $1 !~ /^Benchmark[A-Za-z0-9_/]+$/ ||
				$2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/) {
				printf "invalid allocation budget at line %d: %s\n", FNR, $0 > "/dev/stderr"
				failed = 1
				next
			}
			if ($1 in max_bytes) {
				printf "duplicate allocation budget: %s\n", $1 > "/dev/stderr"
				failed = 1
				next
			}
			max_bytes[$1] = $2
			max_allocs[$1] = $3
			budget_count++
			next
		}
AWK
}

# benchmark_result_rules prints the awk rules that check each benchmark result against its budget.
benchmark_result_rules() {
	cat <<'AWK'
		$1 ~ /^Benchmark/ {
			name = $1
			sub(/-[0-9]+$/, "", name)
			if (!(name in max_bytes)) {
				printf "benchmark has no allocation budget: %s\n", name > "/dev/stderr"
				failed = 1
				next
			}
			if (++seen[name] > 1) {
				printf "duplicate benchmark result: %s\n", name > "/dev/stderr"
				failed = 1
				next
			}

			bytes = ""
			allocs = ""
			for (i = 2; i <= NF; i++) {
				if ($i == "B/op" && i > 1) {
					bytes = $(i - 1)
				}
				if ($i == "allocs/op" && i > 1) {
					allocs = $(i - 1)
				}
			}
			if (bytes !~ /^[0-9]+$/ || allocs !~ /^[0-9]+$/) {
				printf "benchmark lacks integer allocation metrics: %s\n", name > "/dev/stderr"
				failed = 1
				next
			}
			if (bytes > max_bytes[name]) {
				printf "%s exceeds byte budget: %d B/op > %d B/op\n",
					name, bytes, max_bytes[name] > "/dev/stderr"
				failed = 1
			}
			if (allocs > max_allocs[name]) {
				printf "%s exceeds allocation budget: %d allocs/op > %d allocs/op\n",
					name, allocs, max_allocs[name] > "/dev/stderr"
				failed = 1
			}
		}
AWK
}

# budget_coverage_rules prints the awk rules that fail on an empty manifest or a missing result.
budget_coverage_rules() {
	cat <<'AWK'
		END {
			if (budget_count == 0) {
				printf "allocation budget manifest has no entries\n" > "/dev/stderr"
				failed = 1
			}
			for (name in max_bytes) {
				if (!(name in seen)) {
					printf "missing benchmark result: %s\n", name > "/dev/stderr"
					failed = 1
				}
			}
			exit failed
		}
AWK
}

check_output() {
	local output="$1"
	local budgets="$2"
	if [[ ! -s "$output" ]]; then
		printf 'allocation benchmark output is empty: %s\n' "$output" >&2
		return 1
	fi
	if [[ ! -s "$budgets" ]]; then
		printf 'allocation budget manifest is empty: %s\n' "$budgets" >&2
		return 1
	fi

	local program
	program="$(budget_manifest_rules)"$'\n'"$(benchmark_result_rules)"$'\n'"$(budget_coverage_rules)"
	LC_ALL=C awk "$program" "$budgets" "$output"
}

run_benchmarks() {
	local root="$1"
	local output="$2"
	local go_bin="${GO_BIN:-go}"
	(
		cd "$root/core"
		"$go_bin" test -run '^$' -bench '^BenchmarkNew(UUID|KSUID)$' \
			-benchmem -count=1 ./id
		"$go_bin" test -run '^$' -bench '^Benchmark(HashPassword|SealOpen)$' \
			-benchmem -count=1 ./crypto
		cd "$root"
		"$go_bin" test -run '^$' -bench '^BenchmarkTypedSetGet$' \
			-benchmem -count=1 ./cache/memory
	) | tee "$output"
}

main() {
	if [[ $# -eq 4 && "$1" == --check-output && "$3" == --budgets ]]; then
		check_output "$2" "$4"
		return
	fi

	local root
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if [[ $# -eq 2 && "$1" == --root ]]; then
		root="$(cd "$2" && pwd)"
	elif [[ $# -ne 0 ]]; then
		usage
		return 2
	fi
	readonly root

	allocation_suite_root="$(mktemp -d \
		"${TMPDIR:-/tmp}/golusoris-allocation-run.XXXXXX")"
	readonly allocation_suite_root
	trap 'find "$allocation_suite_root" -depth -delete' EXIT

	local output="$allocation_suite_root/benchmark.txt"
	local budgets="$root/scripts/ci/allocation-budgets.tsv"
	run_benchmarks "$root" "$output"
	check_output "$output" "$budgets"
	printf 'Allocation budgets passed: %s\n' "$budgets"
}

main "$@"
