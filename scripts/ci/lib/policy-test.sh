# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# Shared copy deduplicates expect_failure across suites (one implementation); sourced so callers own strict mode.
# shellcheck shell=bash

# Suite root with EXIT cleanup so a failing suite leaves no temp dirs.
policy_suite_init() {
	local name="$1"
	suite_root="$(mktemp -d "${TMPDIR:-/tmp}/$name.XXXXXX")"
	readonly suite_root
	trap 'find "$suite_root" -depth -delete' EXIT
}

# Output discarded because only the status matters.
expect_failure() {
	local status=0
	"$@" >/dev/null 2>&1 || status=$?
	if (( status == 0 )); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
}

# Callers with extra output checks read it without re-running the command.
policy_failure_output=""

# Pattern check so a suite fails for the intended reason, not any error.
expect_failure_matching() {
	local pattern="$1"
	shift
	local status=0
	policy_failure_output="$("$@" 2>&1)" || status=$?
	if (( status == 0 )); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
	if [[ "$policy_failure_output" != *"$pattern"* ]]; then
		printf 'missing failure %q in: %s\n' "$pattern" "$policy_failure_output" >&2
		return 1
	fi
}
