#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Planted cases for scripts/ci/release-pr-ci.sh against a stub gh, so approval and retry regressions fail here, not in a release.
#   usage: scripts/ci/release-pr-ci_test.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
export WORK
trap 'rm -rf "$WORK"' EXIT

SHA=0123456789abcdef0123456789abcdef01234567

# The stub answers from STUB_HEADS and STUB_RUNS, counts run lookups, and logs every approval.
cat > "$WORK/gh" <<'STUB'
#!/usr/bin/env bash
case "$1" in
	pr)
		pr_file="$WORK/pr_lookups"
		pr_count=0
		if [ -f "$pr_file" ]; then
			pr_count="$(cat "$pr_file")"
		fi
		pr_count=$((pr_count + 1))
		printf '%s\n' "$pr_count" > "$pr_file"
		if [ "$pr_count" -le "${STUB_PR_EMPTY_FIRST:-0}" ]; then
			exit 0
		fi
		if [ "$pr_count" -le "${STUB_PR_FAIL_FIRST:-0}" ]; then
			printf '%s\n' "${STUB_PR_FAIL_MSG:-HTTP 502: Bad Gateway}" >&2
			exit 1
		fi
		if [ "$STUB_HEADS" = fail ]; then
			if [ -n "${STUB_PR_FAIL_MSG:-}" ]; then
				printf '%s\n' "$STUB_PR_FAIL_MSG" >&2
			fi
			exit 1
		fi
		printf '%s' "$STUB_HEADS"
		;;
	api)
		if [ "$2" = --method ]; then
			[ "$STUB_APPROVE" = fail ] && exit 1
			printf '%s\n' "$4" >> "$STUB_LOG"
			exit 0
		fi
		count_file="$WORK/run_lookups"
		count=0
		if [ -f "$count_file" ]; then
			count="$(cat "$count_file")"
		fi
		count=$((count + 1))
		printf '%s\n' "$count" > "$count_file"
		if [ "$count" -le "${STUB_EMPTY_FIRST:-0}" ]; then
			exit 0
		fi
		[ "$STUB_RUNS" = fail ] && exit 1
		printf '%s' "$STUB_RUNS"
		;;
	*) exit 64 ;;
esac
STUB
chmod 0755 "$WORK/gh"
# Records each delay instead of waiting it out.
mkdir -p "$WORK/bin"
cat > "$WORK/bin/sleep" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$1" >> "$WORK/sleeps"
STUB
chmod 0755 "$WORK/bin/sleep"

# run_case NAME HEADS RUNS WANT_RC WANT_APPROVALS [APPROVE] [TRIES, empty = script default]
run_case() {
	local name="$1" rc=0 got tries=2 empty_first="${STUB_EMPTY_FIRST:-0}"
	if [ "$#" -ge 7 ]; then
		tries="$7"
	fi
	: > "$WORK/log"
	rm -f "$WORK/run_lookups" "$WORK/pr_lookups"
	local env_args=()
	if [ -z "$tries" ]; then
		env_args+=(-u RELEASE_PR_CI_TRIES)
	else
		env_args+=(RELEASE_PR_CI_TRIES="$tries")
	fi
	env_args+=(
		GH_BIN="$WORK/gh"
		STUB_HEADS="$2"
		STUB_RUNS="$3"
		STUB_LOG="$WORK/log"
		STUB_APPROVE="${6:-ok}"
		STUB_EMPTY_FIRST="$empty_first"
		STUB_PR_EMPTY_FIRST="${STUB_PR_EMPTY_FIRST:-0}"
		STUB_PR_FAIL_FIRST="${STUB_PR_FAIL_FIRST:-0}"
		STUB_PR_FAIL_MSG="${STUB_PR_FAIL_MSG:-}"
		RELEASE_PR_EXPECTED="${RELEASE_PR_EXPECTED:-false}"
		RELEASE_PR_CI_WAIT=0
		RELEASE_PR_CI_LIST_BACKOFF="${LIST_BACKOFF:-0}"
		PATH="$WORK/bin:$PATH"
	)
	env "${env_args[@]}" bash "$HERE/release-pr-ci.sh" owner/repo > "$WORK/out" 2>&1 || rc=$?
	got="$(wc -l < "$WORK/log")"
	if [ "$rc" -ne "$4" ] || [ "$got" -ne "$5" ]; then
		printf 'FAIL %s: exit %s with %s approvals, want exit %s with %s\n' "$name" "$rc" "$got" "$4" "$5" >&2
		cat "$WORK/out" >&2
		exit 1
	fi
	echo "ok   $name: exit $rc, $got approvals"
}

HEAD="release-please--branches--main $SHA false"

run_case waiting "$HEAD" "11 action_required release-please--branches--main owner/repo
12 action_required release-please--branches--main owner/repo" 0 2
grep -qx 'repos/owner/repo/actions/runs/11/approve' "$WORK/log"
grep -qx 'repos/owner/repo/actions/runs/12/approve' "$WORK/log"
run_case one-of-two "$HEAD" "11 success release-please--branches--main owner/repo
12 action_required release-please--branches--main owner/repo" 0 1
grep -qx 'repos/owner/repo/actions/runs/12/approve' "$WORK/log"
run_case already-ran "$HEAD" "11 success release-please--branches--main owner/repo
12 none release-please--branches--main owner/repo" 0 0
run_case other-branch "$HEAD" "11 action_required other-branch owner/repo" 0 0
run_case other-repository "$HEAD" "11 action_required release-please--branches--main fork/repo" 0 0
run_case no-release-pr "" "" 0 0
run_case non-release-head "feature/x $SHA false" "11 action_required feature/x owner/repo" 0 0
run_case cross-repository-head "release-please--branches--main $SHA true" "11 action_required release-please--branches--main owner/repo" 0 0
run_case no-run "$HEAD" "" 1 0
grep -q 'no pull request run on release-please--branches--main' "$WORK/out"
run_case api-fails "$HEAD" fail 1 0
run_case pr-list-fails fail "11 action_required release-please--branches--main owner/repo" 1 0
grep -q 'could not list open release pull requests' "$WORK/out"
STUB_PR_FAIL_FIRST=1 run_case pr-list-retry-502 "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1
grep -qx 'repos/owner/repo/actions/runs/11/approve' "$WORK/log"
[ "$(< "$WORK/pr_lookups")" -eq 2 ]
STUB_PR_FAIL_FIRST=3 run_case pr-list-retry-exhausted "$HEAD" "11 action_required release-please--branches--main owner/repo" 1 0
[ "$(< "$WORK/pr_lookups")" -eq 3 ]
grep -q 'could not list open release pull requests' "$WORK/out"
grep -q 'HTTP 502: Bad Gateway' "$WORK/out"
STUB_PR_FAIL_FIRST=1 STUB_PR_FAIL_MSG="HTTP 404: Not Found" run_case pr-list-non-transient "$HEAD" "11 action_required release-please--branches--main owner/repo" 1 0
[ "$(< "$WORK/pr_lookups")" -eq 1 ]
grep -q 'could not list open release pull requests' "$WORK/out"
grep -q 'HTTP 404: Not Found' "$WORK/out"
STUB_PR_FAIL_FIRST=1 STUB_PR_FAIL_MSG="HTTP 404: Not Found (https://api.github.com/repos/owner/repo/pulls/503?x=504)" run_case pr-list-non-transient-digits "$HEAD" "11 action_required release-please--branches--main owner/repo" 1 0
[ "$(< "$WORK/pr_lookups")" -eq 1 ]
STUB_PR_FAIL_FIRST=1 STUB_PR_FAIL_MSG="HTTP 503: Service Unavailable" run_case pr-list-retry-503 "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1
[ "$(< "$WORK/pr_lookups")" -eq 2 ]
STUB_PR_FAIL_FIRST=1 STUB_PR_FAIL_MSG="HTTP 403: API rate limit exceeded" run_case pr-list-retry-rate-limit "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1
[ "$(< "$WORK/pr_lookups")" -eq 2 ]
STUB_PR_FAIL_FIRST=1 STUB_PR_FAIL_MSG="dial tcp: lookup api.github.com: no such host" run_case pr-list-retry-dns "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1
[ "$(< "$WORK/pr_lookups")" -eq 2 ]
: > "$WORK/sleeps"
STUB_PR_FAIL_FIRST=1 LIST_BACKOFF=08 run_case pr-list-backoff-decimal "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1
sleep_arg="$(head -n 1 "$WORK/sleeps")"
[ "$sleep_arg" -ge 8 ] && [ "$sleep_arg" -le 15 ]
run_case approve-fails "$HEAD" "11 action_required release-please--branches--main owner/repo" 1 0 fail
run_case odd-run "$HEAD" "null action_required release-please--branches--main owner/repo" 1 0
run_case odd-head "release-please--branches--main not-a-sha false" "11 action_required release-please--branches--main owner/repo" 1 0
STUB_EMPTY_FIRST=1 run_case late-run "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1 ok 2
grep -qx 'repos/owner/repo/actions/runs/11/approve' "$WORK/log"
STUB_EMPTY_FIRST=2 run_case too-late "$HEAD" "11 action_required release-please--branches--main owner/repo" 1 0 ok 2
grep -q 'after 2 tries' "$WORK/out"
STUB_EMPTY_FIRST=10 run_case default-window "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1 ok ""
grep -qx 'repos/owner/repo/actions/runs/11/approve' "$WORK/log"

RELEASE_PR_EXPECTED=true STUB_PR_EMPTY_FIRST=1 run_case late-release-pr "$HEAD" "11 action_required release-please--branches--main owner/repo" 0 1
grep -qx 'repos/owner/repo/actions/runs/11/approve' "$WORK/log"
RELEASE_PR_EXPECTED=true run_case expected-pr-missing "" "" 1 0
grep -q 'none listed after 2 tries' "$WORK/out"
RELEASE_PR_EXPECTED=yes run_case odd-expected "$HEAD" "11 action_required release-please--branches--main owner/repo" 2 0

echo "release pull request CI: 7 approved, 6 skipped, 11 refused"
