#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# CHECK may name a mutant so the same assertions prove the diagnostic regression.
readonly CHECK="${CHECK:-$SCRIPT_DIR/sqlite-probe-load-summary.sh}"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-sqlite-load-summary.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

expect_output() {
	local name="$1" input="$2" expected="$3" output status=0
	output="$(bash "$CHECK" "$input" 2>&1)" || status="$?"
	if [[ "$status" -ne 0 || "$output" != "$expected" ]]; then
		printf '%s: expected exit 0 and output:\n%s\ngot exit %d:\n%s\n' \
			"$name" "$expected" "$status" "$output" >&2
		exit 1
	fi
	printf 'PASS %s\n' "$name"
}

expect_missing() {
	local name="$1" input="$2" output status=0
	output="$(bash "$CHECK" "$input" 2>&1)" || status="$?"
	if [[ "$status" -ne 2 || "$output" != *'missing or unreadable load log:'* ]]; then
		printf '%s: expected missing/unreadable exit 2, got %d: %s\n' \
			"$name" "$status" "$output" >&2
		exit 1
	fi
	printf 'PASS %s\n' "$name"
}

timeout_log="$suite_root/timeout log"
printf '%s\n' 'panic: test timed out after 10m0s' 'running tests:' \
	$'\tTestA (9m58s)' $'\tTestB (1m2s)' '' 'goroutine 123 [running]:' \
	$'\tstack detail omitted' \
	'FAIL github.com/golusoris/golusoris/storage/storagetest 600.154s' >"$timeout_log"
expect_output timeout_names_and_durations "$timeout_log" \
	$'FAIL lines:\nFAIL github.com/golusoris/golusoris/storage/storagetest 600.154s\nGo test timeouts:\npanic: test timed out after 10m0s\nrunning tests:\n\tTestA (9m58s)\n\tTestB (1m2s)\nPackage results (slowest first):\nFAIL github.com/golusoris/golusoris/storage/storagetest 600.154s'

ok_log="$suite_root/ok.log"
printf '%s\n' $'ok\tpkg/fast\t2.001s' 'ok pkg/slow 12.100s coverage: 80.0% of statements' \
	'ok pkg/cached (cached)' '? pkg/no-tests [no test files]' >"$ok_log"
expect_output all_ok_without_panic "$ok_log" \
	$'Package results (slowest first):\nok pkg/slow 12.100s coverage: 80.0% of statements\nok\tpkg/fast\t2.001s'

fail_log="$suite_root/fail.log"
printf '%s\n' '--- FAIL: TestFailure (0.00s)' 'FAIL pkg/failure [build failed]' 'FAIL' >"$fail_log"
expect_output fail_only_without_panic "$fail_log" \
	$'FAIL lines:\n--- FAIL: TestFailure (0.00s)\nFAIL pkg/failure [build failed]\nFAIL'

block_log="$suite_root/block.log"
printf '%s\n' 'panic: test timed out after 10m0s' 'running tests:' >"$block_log"
for ((index = 1; index <= 40; index++)); do
	printf '\tTestBound%d (1s)\n' "$index" >>"$block_log"
done
block_expected="Go test timeouts:"$'\n'"$(cat "$block_log")"
expect_output running_tests_exactly_40 "$block_log" "$block_expected"
printf '\tTestBound41 (1s)\n' >>"$block_log"
expect_output running_tests_capped_at_40 "$block_log" "$block_expected"
printf '%s\n' '' 'goroutine 1 [running]:' $'\tTestNotRunning (1s)' \
	'panic: test timed out after 2m0s' 'running tests:' $'\tTestOtherPackage (1m0s)' >>"$block_log"
expect_output multiple_timeout_blocks "$block_log" \
	"$block_expected"$'\npanic: test timed out after 2m0s\nrunning tests:\n\tTestOtherPackage (1m0s)'

fail_expected='FAIL lines:'
: >"$fail_log"
for ((index = 1; index <= 41; index++)); do
	printf '%s\n' "--- FAIL: TestFailure$index (0.00s)" >>"$fail_log"
	if ((index <= 40)); then
		fail_expected+=$'\n'"--- FAIL: TestFailure$index (0.00s)"
	fi
done
expect_output fail_lines_capped_at_40 "$fail_log" "$fail_expected"

packages_expected='Package results (slowest first):'
: >"$ok_log"
for ((index = 16; index >= 1; index--)); do
	printf '%s\n' "ok pkg/duration$index $index.001s" >>"$ok_log"
	if ((index >= 2)); then
		packages_expected+=$'\n'"ok pkg/duration$index $index.001s"
	fi
done
expect_output packages_top_15 "$ok_log" "$packages_expected"

: >"$ok_log"
expect_output empty_log "$ok_log" ''
expect_missing missing_file "$suite_root/missing.log"
expect_missing directory_input "$suite_root"

printf 'sqlite probe load summary tests passed\n'
