#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly WORKFLOW="$SCRIPT_DIR/../../.github/workflows/sqlite-stall-probe.yml"
readonly SUMMARY="bash scripts/ci/sqlite-probe-load-summary.sh \"\$RUNNER_TEMP/load.log\" || echo \"load summary failed\" >&2"
# Issue 1222: storagetest outran the 10m default, so file-load needs at least 11m.
readonly MIN_LOAD_TIMEOUT=11
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-sqlite-stall-policy.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

check_workflow() {
	local workflow="$1" job_timeout load_block load_command load_timeout invalid_line
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
	invalid_line="$(printf '%s\n' "$load_block" | awk -v summary="$SUMMARY" '
		NR > 1000 { exit 1 }
		/^ *#/ { next }
		# Each shape carries its step, so a moved result reset cannot hide a failed probe or load.
		{ step = 0 }
		/^            export STALLPROBE_P=4 STALLPROBE_N=600$/ { step = 1 }
		# Exact flag allow-list: Go keeps the last flag value, so a second timeout in any spelling would override the checked one.
		/^            go test -short -count=[1-9][0-9]*( -timeout [1-9][0-9]*m)?( \.\/[a-z0-9_][a-z0-9_\/]*\/\.\.\.)+ > "\$RUNNER_TEMP\/load\.log" 2>&1 &$/ { step = 2 }
		/^            load_pid=\$!$/ { step = 3 }
		/^            probe_rc=0$/ { step = 4 }
		# A "#" inside a go test line would comment out its tail, so it is excluded with the separators.
		/^            go test [^;&|#]* \|\| probe_rc=\$\?$/ { step = 5 }
		/^            load_rc=0$/ { step = 6 }
		/^            wait "\$load_pid" \|\| load_rc=\$\?$/ { step = 7 }
		/^            echo "probe_rc=\$probe_rc load_rc=\$load_rc"$/ { step = 8 }
		$0 == "            " summary { step = 9 }
		/^            \[\[ "\$probe_rc" == 0 && "\$load_rc" == 0 \]\]$/ { step = 10 }
		step == 0 { print "file-load branch line outside the straight-line allow-list: " $0; failed = 1; exit }
		step != last + 1 { print "file-load branch line out of order (want step " last + 1 ", got step " step "): " $0; failed = 1; exit }
		{ last = step }
		END { if (!failed && last != 10) print "file-load branch stops after step " last " of 10" }
	')"
	if [[ -n "$invalid_line" ]]; then
		printf '%s\n' "$invalid_line" >&2
		return 1
	fi
	load_command="$(printf '%s\n' "$load_block" | sed -n '/^            go test .*\.\/storage\/.* &$/p')"
	if [[ ! "$load_command" =~ [[:space:]]-timeout[[:space:]]([1-9][0-9]{0,3})m[[:space:]] ]]; then
		printf 'file-load go test requires explicit -timeout <N>m\n' >&2
		return 1
	fi
	load_timeout="${BASH_REMATCH[1]}"
	if ((load_timeout < MIN_LOAD_TIMEOUT)); then
		printf 'file-load -timeout must exceed the 10m default storagetest outran (issue 1222)\n' >&2
		return 1
	fi
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

# Writes the workflow with exact line $1 moved to just after exact line $2.
move_line_after() {
	awk -v moved="$1" -v anchor="$2" '
		NR > 1000 { exit 1 }
		$0 == moved { held = $0; next }
		{ print }
		$0 == anchor { print held }
	' "$WORKFLOW" >"$3"
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
	"file-load branch line outside the straight-line allow-list:             if [[ \"\$load_rc\" != 0 ]]; then $SUMMARY; fi"

sed -E 's/ -timeout [0-9]+m/ -timeout 10m/' "$WORKFLOW" >"$suite_root/load-timeout-default.yml"
expect_failure load_timeout_default "$suite_root/load-timeout-default.yml" \
	'file-load -timeout must exceed the 10m default storagetest outran (issue 1222)'

sed -E -e 's/ -timeout [0-9]+m/ -timeout 1m/' \
	-e 's/^(    timeout-minutes:) [0-9]+$/\1 7/' \
	"$WORKFLOW" >"$suite_root/load-timeout-tiny.yml"
expect_failure load_timeout_tiny "$suite_root/load-timeout-tiny.yml" \
	'file-load -timeout must exceed the 10m default storagetest outran (issue 1222)'

awk '
	NR > 1000 { exit 1 }
	$0 == "            echo \"probe_rc=$probe_rc load_rc=$load_rc\"" {
		print "            if [[ -n \"${SKIP:-}\" ]]; then"
	}
	{ print }
	$0 == "            [[ \"$probe_rc\" == 0 && \"$load_rc\" == 0 ]]" {
		print "            fi"
	}
' "$WORKFLOW" >"$suite_root/enclosing-if.yml"
expect_failure enclosing_if "$suite_root/enclosing-if.yml" \
	"file-load branch line outside the straight-line allow-list:             if [[ -n \"\${SKIP:-}\" ]]; then"

awk '
	NR > 1000 { exit 1 }
	$0 == "            echo \"probe_rc=$probe_rc load_rc=$load_rc\"" { $0 = $0 " && exit 0" }
	{ print }
' "$WORKFLOW" >"$suite_root/chained-exit.yml"
expect_failure chained_exit "$suite_root/chained-exit.yml" \
	"file-load branch line outside the straight-line allow-list:             echo \"probe_rc=\$probe_rc load_rc=\$load_rc\" && exit 0"

awk -v summary="$SUMMARY" '
	NR > 1000 { exit 1 }
	$0 == "            " summary { $0 = "            true; " summary }
	{ print }
' "$WORKFLOW" >"$suite_root/semicolon.yml"
expect_failure semicolon "$suite_root/semicolon.yml" \
	"file-load branch line outside the straight-line allow-list:             true; $SUMMARY"

awk '
	NR > 1000 { exit 1 }
	/^            go test -short / { sub(/ \.\/storage\//, " \\\n            ./storage/") }
	{ print }
' "$WORKFLOW" >"$suite_root/continuation.yml"
expect_failure continuation "$suite_root/continuation.yml" \
	"file-load branch line outside the straight-line allow-list:             go test -short -count=3 -timeout ${load_timeout}m \\"

awk '
	NR > 1000 { exit 1 }
	$0 == "            echo \"probe_rc=$probe_rc load_rc=$load_rc\"" { print "            exit 0" }
	{ print }
' "$WORKFLOW" >"$suite_root/early-exit.yml"
expect_failure early_exit "$suite_root/early-exit.yml" \
	'file-load branch line outside the straight-line allow-list:             exit 0'

move_line_after "            echo \"probe_rc=\$probe_rc load_rc=\$load_rc\"" "            $SUMMARY" \
	"$suite_root/reordered-summary.yml"
expect_failure reordered_summary "$suite_root/reordered-summary.yml" \
	"file-load branch line out of order (want step 8, got step 9):             $SUMMARY"

probe_line="$(sed -n '/^            go test .* || probe_rc=\$?$/p' "$WORKFLOW")"
move_line_after "            probe_rc=0" "$probe_line" "$suite_root/moved-probe-init.yml"
expect_failure moved_probe_init "$suite_root/moved-probe-init.yml" \
	"file-load branch line out of order (want step 4, got step 5): $probe_line"

wait_line="            wait \"\$load_pid\" || load_rc=\$?"
move_line_after "            load_rc=0" "$wait_line" "$suite_root/moved-load-init.yml"
expect_failure moved_load_init "$suite_root/moved-load-init.yml" \
	"file-load branch line out of order (want step 6, got step 7): $wait_line"

grep -vxF "            [[ \"\$probe_rc\" == 0 && \"\$load_rc\" == 0 ]]" "$WORKFLOW" >"$suite_root/truncated.yml"
expect_failure truncated_result "$suite_root/truncated.yml" 'file-load branch stops after step 9 of 10'

sed -E 's/^(            go test .*\.\/db\/sqlite\/) \|\| probe_rc=/\1 # || probe_rc=/' \
	"$WORKFLOW" >"$suite_root/hash-in-probe.yml"
probe_line="$(sed -n '/^            go test .* # || probe_rc=\$?$/p' "$suite_root/hash-in-probe.yml")"
expect_failure hash_in_probe "$suite_root/hash-in-probe.yml" \
	"file-load branch line outside the straight-line allow-list: $probe_line"

for plant in 'dup_timeout_space:-timeout 5m' 'dup_timeout_equals:-timeout=5m' 'dup_timeout_alias:-test.timeout=5m'; do
	name="${plant%%:*}"
	sed -E "s/ -timeout ([0-9]+)m / -timeout \\1m ${plant#*:} /" "$WORKFLOW" >"$suite_root/$name.yml"
	load_line="$(grep -E '^            go test -short .* &$' "$suite_root/$name.yml")"
	expect_failure "$name" "$suite_root/$name.yml" \
		"file-load branch line outside the straight-line allow-list: $load_line"
done

printf 'sqlite stall probe policy tests passed\n'
