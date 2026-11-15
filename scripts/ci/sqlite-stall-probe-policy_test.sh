#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly WORKFLOW="$SCRIPT_DIR/../../.github/workflows/sqlite-stall-probe.yml"
readonly SUMMARY="bash scripts/ci/sqlite-probe-load-summary.sh \"\$RUNNER_TEMP/load.log\" || echo \"load summary failed\" >&2"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-sqlite-stall-policy.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

check_workflow() {
	local workflow="$1" job_timeout load_block load_command load_timeout
	job_timeout="$(awk '
		NR > 1000 { exit }
		/^  probe:$/ { probe = 1; next }
		/^  [^ ]/ { probe = 0 }
		probe && /^    timeout-minutes: [0-9]+$/ { print $2 }
	' "$workflow")"
	load_block="$(awk '
		NR > 1000 { exit }
		/^          if \[\[ "\$VARIANT" == "file-load" \]\]; then$/ { load = 1; next }
		/^          else$/ { load = 0 }
		load { print }
	' "$workflow")"
	load_command="$(printf '%s\n' "$load_block" | sed -n '/^            go test .*\.\/storage\/.* &$/p')"
	if [[ ! "$load_command" =~ [[:space:]]-timeout[[:space:]]([1-9][0-9]{0,3})m[[:space:]] ]]; then
		printf 'file-load go test requires explicit -timeout <N>m\n' >&2
		return 1
	fi
	load_timeout="${BASH_REMATCH[1]}"
	if [[ ! "$job_timeout" =~ ^[1-9][0-9]{0,3}$ ]] || ((job_timeout <= load_timeout + 5)); then
		printf 'job timeout-minutes must exceed load timeout %sm plus 5\n' "$load_timeout" >&2
		return 1
	fi
	if [[ "$load_block" != *$'\n            echo "probe_rc=$probe_rc load_rc=$load_rc"\n            '"$SUMMARY"$'\n            [[ "$probe_rc" == 0 && "$load_rc" == 0 ]]' ]]; then
		printf 'file-load summary must be unconditional before final probe_rc/load_rc result\n' >&2
		return 1
	fi
}

expect_failure() {
	local name="$1" workflow="$2" reason="$3" output status=0
	output="$(check_workflow "$workflow" 2>&1)" || status="$?"
	if [[ "$status" -ne 1 || "$output" != "$reason" ]]; then
		printf '%s: expected exit 1 and %s; got exit %d: %s\n' \
			"$name" "$reason" "$status" "$output" >&2
		exit 1
	fi
	printf 'PASS %s: rc=%d: %s\n' "$name" "$status" "$output"
}

check_workflow "$WORKFLOW"
printf 'PASS real_workflow\n'

sed -E 's/ -timeout [0-9]+m//' "$WORKFLOW" >"$suite_root/no-timeout.yml"
expect_failure missing_load_timeout "$suite_root/no-timeout.yml" \
	'file-load go test requires explicit -timeout <N>m'

load_timeout="$(sed -nE 's/^            go test .* -timeout ([0-9]+)m .*/\1/p' "$WORKFLOW")"
sed -E "s/^(    timeout-minutes:) [0-9]+$/\1 $load_timeout/" \
	"$WORKFLOW" >"$suite_root/job-timeout.yml"
expect_failure insufficient_job_timeout "$suite_root/job-timeout.yml" \
	"job timeout-minutes must exceed load timeout ${load_timeout}m plus 5"

awk -v summary="$SUMMARY" '
	NR > 1000 { exit 1 }
	$0 == "            " summary {
		print "            if [[ \"$load_rc\" != 0 ]]; then " summary "; fi"
		next
	}
	{ print }
' "$WORKFLOW" >"$suite_root/conditional-summary.yml"
expect_failure conditional_summary "$suite_root/conditional-summary.yml" \
	'file-load summary must be unconditional before final probe_rc/load_rc result'

printf 'sqlite stall probe policy tests passed\n'
