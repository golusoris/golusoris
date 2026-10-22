#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Planted cases for scripts/ci/require-green-ci.sh against a stub gh: a green
# check passes, also when it appears only after a long-running CI run (the
# v0.20.0 failure); a red check, a CI run that completed without the check, no
# CI run past the grace and a check that never concludes fail.
#   usage: scripts/ci/require-green-ci_test.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
export WORK
trap 'rm -rf "$WORK"' EXIT

# The stub answers check lookups with STUB_CHECK after STUB_MISSING_FIRST
# "missing none" answers, and run lookups with STUB_RUN.
cat > "$WORK/gh" <<'STUB'
#!/usr/bin/env bash
[ "$1" = api ] || exit 64
case "$2" in
*/check-runs\?*)
	count_file="$WORK/check_lookups"
	count=0
	if [ -f "$count_file" ]; then
		count="$(cat "$count_file")"
	fi
	count=$((count + 1))
	printf '%s\n' "$count" > "$count_file"
	if [ "$count" -le "${STUB_MISSING_FIRST:-0}" ]; then
		printf 'missing none\n'
	else
		printf '%s\n' "$STUB_CHECK"
	fi
	;;
*/actions/workflows/*/runs\?*) printf '%s\n' "$STUB_RUN" ;;
*) exit 64 ;;
esac
STUB
chmod 0755 "$WORK/gh"

failures=0

# run_case NAME CHECK MISSING_FIRST RUN WANT_RC
run_case() {
	local name="$1" rc=0
	rm -f "$WORK/check_lookups"
	PATH="$WORK:$PATH" STUB_CHECK="$2" STUB_MISSING_FIRST="$3" STUB_RUN="$4" \
		SHA=0123456789abcdef0123456789abcdef01234567 CHECK_NAME="CI success" \
		GITHUB_REPOSITORY=owner/repo POLL_INTERVAL_SECONDS=0 POLL_ATTEMPTS=6 \
		MISSING_GRACE_ATTEMPTS=2 \
		bash "$HERE/require-green-ci.sh" > "$WORK/out" 2>&1 || rc=$?
	if [ "$rc" -ne "$5" ]; then
		echo "FAIL ${name}: rc=${rc}, want $5"
		cat "$WORK/out"
		failures=$((failures + 1))
		return
	fi
	echo "ok   ${name}"
}

run_case "green check" "completed success" 0 in_progress 0
run_case "check created after a long CI run" "completed success" 5 in_progress 0
run_case "check created while CI is queued" "completed success" 4 queued 0
run_case "red check" "completed failure" 0 completed 1
run_case "CI run completed without the check" "completed success" 6 completed 1
run_case "no CI run past the grace" "completed success" 6 none 1
run_case "no CI run within the grace" "completed success" 2 none 0
run_case "check never concludes" "in_progress none" 0 in_progress 1

if [ "$failures" -ne 0 ]; then
	echo "${failures} case(s) failed"
	exit 1
fi
echo "require-green-ci: all cases passed"
