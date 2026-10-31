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


# Workflows call the composite at a full commit SHA instead of inlining the step.
check_stop_on_draft_usage() {
	local workflow="$1" status=0
	if grep -Fq 'Gate not run on a draft' "$workflow"; then
		printf '%s must use the stop-on-draft composite action instead of an inline draft block\n' "${workflow##*/}" >&2
		status=1
	fi
	if grep -E 'stop-on-draft@' "$workflow" | grep -Evq 'stop-on-draft@[0-9a-f]{40}([^0-9a-f]|$)'; then
		printf '%s: stop-on-draft must be pinned to a full 40-character SHA\n' "${workflow##*/}" >&2
		status=1
	fi
	return "$status"
}

# praetorctl adopt emits its managed workflows with the inline step and audit locks them byte for byte.
praetor_managed="$(python3 -c 'import json, sys
rules = json.load(open(sys.argv[1], encoding="utf-8"))["packageRules"]
for rule in rules:
    if rule.get("description", "").startswith("praetor-managed files"):
        print("\n".join(rule["matchFileNames"]))' "$repo_root/renovate.json")"
if [[ "$praetor_managed" != *.github/workflows/* ]]; then
	printf 'renovate.json praetor-managed rule lists no workflow\n' >&2
	exit 1
fi

for workflow in "$repo_root/.github/workflows"/*.yml "$repo_root/.github/workflows"/*.yaml; do
	[[ -f "$workflow" ]] || continue
	if grep -Fqx ".github/workflows/${workflow##*/}" <<<"$praetor_managed"; then
		continue
	fi
	check_stop_on_draft_usage "$workflow"
done

# Negative case 5: inline draft block in a workflow.
neg_inline="$suite_root/inline-block.yml"
sed '0,/uses: golusoris\/golusoris\/\.github\/actions\/stop-on-draft@/s||env:\n          TITLE: Gate not run on a draft\n        uses: golusoris/golusoris/.github/actions/stop-on-draft@|' \
	"$repo_root/.github/workflows/ci.yml" >"$neg_inline"
expect_failure 'instead of an inline draft block' check_stop_on_draft_usage "$neg_inline"

# Negative case 6: branch ref instead of a commit SHA.
neg_ref="$suite_root/non-sha-ref.yml"
sed 's|stop-on-draft@[0-9a-f]\{40\}|stop-on-draft@main|' "$repo_root/.github/workflows/ci.yml" >"$neg_ref"
expect_failure 'pinned to a full 40-character SHA' check_stop_on_draft_usage "$neg_ref"

printf 'stop-on-draft policy tests passed\n'
