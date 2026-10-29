#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# shellcheck disable=SC2016 # Match literal GitHub expressions and variables.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-stop-on-draft-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

expect_failure() {
	local pattern="$1"
	shift
	local output status=0
	output="$("$@" 2>&1)" || status=$?
	if ((status == 0)) || [[ "$output" != *"$pattern"* ]]; then
		printf 'expected failure containing %q: %s\n' "$pattern" "$output" >&2
		return 1
	fi
}

check_stop_on_draft_action() {
	local path="$1"
	if [[ ! -f "$path" ]]; then
		printf 'stop-on-draft action missing: %s\n' "$path" >&2
		return 1
	fi
	if ! grep -Fq 'using: composite' "$path"; then
		printf 'stop-on-draft action must declare using: composite: %s\n' "$path" >&2
		return 1
	fi
	if ! grep -Fq 'shell: bash' "$path"; then
		printf 'stop-on-draft action must specify shell: bash: %s\n' "$path" >&2
		return 1
	fi
	if ! grep -Fq 'if: github.event.pull_request.draft == true' "$path"; then
		printf 'stop-on-draft action lacks draft condition: %s\n' "$path" >&2
		return 1
	fi
	if ! grep -Fq '>> "$GITHUB_STEP_SUMMARY"' "$path"; then
		printf 'stop-on-draft action lacks step summary append: %s\n' "$path" >&2
		return 1
	fi
	if ! grep -Eq '^[[:space:]]*exit 1([[:space:]]+.*)?$' "$path"; then
		printf 'stop-on-draft action lacks exit 1: %s\n' "$path" >&2
		return 1
	fi

	local in_run=0 run_indent=0 line indent
	while IFS= read -r line || [[ -n "$line" ]]; do
		if [[ "$line" =~ ^([[:space:]]*)run:[[:space:]]*(\|)? ]]; then
			in_run=1
			run_indent=${#BASH_REMATCH[1]}
			continue
		fi
		if (( in_run )); then
			if [[ -z "${line// /}" ]]; then
				continue
			fi
			[[ "$line" =~ ^([[:space:]]*) ]]
			indent=${#BASH_REMATCH[1]}
			if (( indent <= run_indent )); then
				in_run=0
			elif [[ "$line" == *'${{'* ]]; then
				printf 'stop-on-draft action interpolates expression in run block: %s\n' "$path" >&2
				return 1
			fi
		fi
	done < "$path"
}

action_file="$repo_root/.github/actions/stop-on-draft/action.yml"

# Positive case: real action manifest passes all checks.
check_stop_on_draft_action "$action_file"

# Negative case 1: missing exit 1.
neg_no_exit="$suite_root/no-exit.yml"
sed '/exit 1/d' "$action_file" >"$neg_no_exit"
expect_failure 'lacks exit 1' check_stop_on_draft_action "$neg_no_exit"

# Negative case 2: condition removed.
neg_no_cond="$suite_root/no-cond.yml"
sed '/if: github\.event\.pull_request\.draft == true/d' "$action_file" >"$neg_no_cond"
expect_failure 'lacks draft condition' check_stop_on_draft_action "$neg_no_cond"

# Negative case 3: ${{ }} interpolated in run.
neg_interpolated="$suite_root/interpolated.yml"
sed 's/echo "$MESSAGE"/echo "${{ env.MESSAGE }}"/' "$action_file" >"$neg_interpolated"
expect_failure 'interpolates expression in run block' check_stop_on_draft_action "$neg_interpolated"

# Negative case 4: missing summary append.
neg_no_summary="$suite_root/no-summary.yml"
sed '/GITHUB_STEP_SUMMARY/d' "$action_file" >"$neg_no_summary"
expect_failure 'lacks step summary append' check_stop_on_draft_action "$neg_no_summary"

printf 'stop-on-draft policy tests passed\n'
