#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
readonly repo_root

# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init "golusoris-policy-lib"

# Positive: expect_failure_matching accepts matching output and stores it in policy_failure_output.
if ! expect_failure_matching 'boom' bash -c 'printf boom; exit 3'; then
	printf 'expect_failure_matching rejected matching failing command\n' >&2
	exit 1
fi
if [[ "$policy_failure_output" != 'boom' ]]; then
	printf 'expected policy_failure_output to equal boom, got %q\n' "$policy_failure_output" >&2
	exit 1
fi

# Negative: commands that exit 0 are rejected by expect_failure_matching and expect_failure.
if expect_failure_matching 'boom' bash -c 'printf boom; exit 0' 2>/dev/null; then
	printf 'expect_failure_matching accepted a command that exited 0\n' >&2
	exit 1
fi
if expect_failure bash -c 'exit 0' 2>/dev/null; then
	printf 'expect_failure accepted a command that exited 0\n' >&2
	exit 1
fi

# Negative: failing command whose output lacks the pattern is rejected.
if expect_failure_matching 'target' bash -c 'printf other; exit 1' 2>/dev/null; then
	printf 'expect_failure_matching accepted output lacking pattern\n' >&2
	exit 1
fi

# Boundary: command executes exactly once during failure matching.
count_file="$suite_root/count"
# shellcheck disable=SC2016 # $1 expands in the child shell, which gets the path as an argument.
if ! expect_failure_matching 'ran' bash -c 'printf "tick\n" >> "$1"; printf "ran\n"; exit 1' _ "$count_file"; then
	printf 'expect_failure_matching rejected single-run command\n' >&2
	exit 1
fi
if [[ "$(wc -l <"$count_file")" -ne 1 ]]; then
	printf 'expected count file to hold exactly 1 line\n' >&2
	exit 1
fi

# Positive: expect_failure accepts a non-zero exit status.
if ! expect_failure bash -c 'exit 1'; then
	printf 'expect_failure rejected non-zero exit\n' >&2
	exit 1
fi

printf 'policy-test library tests passed\n'
