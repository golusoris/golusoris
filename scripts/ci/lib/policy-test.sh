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

# Literal string check so required publication controls and flags cannot be omitted.
require_literal() {
	local path="$1"
	local literal="$2"
	if ! grep -Fq -- "$literal" "$path"; then
		printf '%s lacks required publication control: %s\n' "$path" "$literal" >&2
		return 1
	fi
}

# Unconditional step guard so validation and signing cannot be skipped on new or resumed runs.
require_unconditional_step() {
	local path="$1"
	local step="$2"
	local block
	block="$(awk -v marker="      - name: $step" '
		$0 == marker { active = 1; next }
		active && /^      - name:/ { exit }
		active { print }
	' "$path")"
	if [[ -z "$block" ]] || grep -Eq '^        if:' <<<"$block"; then
		printf '%s must run %s on new and resumed publication\n' "$path" "$step" >&2
		return 1
	fi
}
