#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Ported from golusoris/goenvoy tools/release-pr-ci.sh (commit 7b77e31).
# Approve the CI runs that wait on the head of every open release pull request.
# The runs of a pull request opened with the repository token wait for an
# approval and report no check until then, and main requires the CI success
# check.
#   usage: scripts/ci/release-pr-ci.sh OWNER/REPO
#   needs: gh with a token that may read pull requests and write actions
#   env:   RELEASE_PR_CI_TRIES (default 18), RELEASE_PR_CI_WAIT seconds (default 10),
#          RELEASE_PR_CI_LIST_BACKOFF seconds (default 2),
#          RELEASE_PR_EXPECTED=true when release-please reported a pull request
set -euo pipefail

if [ $# -ne 1 ]; then
	echo "usage: ${0##*/} OWNER/REPO" >&2
	exit 2
fi
REPO="$1"
GH="${GH_BIN:-gh}"
TRIES="${RELEASE_PR_CI_TRIES:-18}"
WAIT="${RELEASE_PR_CI_WAIT:-10}"
BASE="${RELEASE_PR_CI_LIST_BACKOFF:-2}"
if [[ ! "$TRIES" =~ ^[1-9][0-9]?$ ]] || [[ ! "$WAIT" =~ ^[0-9]{1,3}$ ]] || [[ ! "$BASE" =~ ^[0-9]{1,2}$ ]]; then
	echo "error: unexpected RELEASE_PR_CI_TRIES, RELEASE_PR_CI_WAIT or RELEASE_PR_CI_LIST_BACKOFF" >&2
	exit 2
fi
# Leading zeros would otherwise read as octal, and 08 or 09 abort the arithmetic.
WAIT=$((10#$WAIT))
BASE=$((10#$BASE))

EXPECTED="${RELEASE_PR_EXPECTED:-false}"
if [ "$EXPECTED" != true ] && [ "$EXPECTED" != false ]; then
	echo "error: unexpected RELEASE_PR_EXPECTED" >&2
	exit 2
fi

readonly LIST_ATTEMPTS=3

# The author depends on the token release-please ran with, so a release branch of this
# repository selects; only writers can push one, and writers may approve runs anyway.
release_heads() {
	local -r transient_regex='HTTP 50[0-9]|HTTP 429|[Rr]ate limit|502 Bad Gateway|503|504|connection reset|connection refused|[Tt]imeout|deadline exceeded|no such host|[Tt]emporary failure in name resolution|EOF|TLS handshake'
	local listed="" branch sha cross attempt err err_file
	err_file="$(mktemp "${TMPDIR:-/tmp}/golusoris-release-pr-ci.XXXXXX")"

	for ((attempt = 1; attempt <= LIST_ATTEMPTS; attempt++)); do
		if listed="$("$GH" pr list --repo "$REPO" --state open --label 'autorelease: pending' \
			--json headRefName,headRefOid,isCrossRepository \
			--jq '.[] | "\(.headRefName) \(.headRefOid) \(.isCrossRepository)"' 2>"$err_file")"; then
			break
		fi
		err="$(cat "$err_file")"
		# Retry only transient forge or network errors, at most LIST_ATTEMPTS times.
		if [[ ! "$err" =~ $transient_regex ]] || [ "$attempt" -eq "$LIST_ATTEMPTS" ]; then
			echo "error: could not list open release pull requests" >&2
			[ -n "$err" ] && printf '%s\n' "$err" >&2
			rm -f "$err_file"
			return 1
		fi
		if [ "$BASE" -gt 0 ]; then
			sleep $((attempt * BASE + RANDOM % BASE))
		fi
	done
	rm -f "$err_file"

	while read -r branch sha cross; do
		if [[ "$branch" =~ ^release-please--[A-Za-z0-9._/-]+$ ]] && [ "$cross" = false ]; then
			printf '%s %s\n' "$branch" "$sha"
		fi
	done <<< "$listed"
}

# A pull request release-please has just opened can be missing from the list for a moment.
HEADS=""
for ((try = 1; try <= TRIES; try++)); do
	HEADS="$(release_heads)"
	if [ -n "$HEADS" ] || [ "$EXPECTED" != true ]; then
		break
	fi
	if [ "$try" -lt "$TRIES" ]; then
		sleep "$WAIT"
	fi
done
if [ -z "$HEADS" ]; then
	if [ "$EXPECTED" = true ]; then
		echo "error: release-please reported a pull request, none listed after $TRIES tries" >&2
		exit 1
	fi
	echo "No open release pull request."
	exit 0
fi

# Prints "ID CONCLUSION BRANCH REPOSITORY" for every pull request run on a head.
runs_on() {
	"$GH" api "repos/$REPO/actions/runs?event=pull_request&head_sha=$1&per_page=100" \
		--jq '.workflow_runs[] | "\(.id) \(.conclusion // "none") \(.head_branch) \(.head_repository.full_name)"'
}

while read -r branch sha; do
	if [[ ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
		echo "error: unexpected head for $branch: $sha" >&2
		exit 1
	fi
	# The runs appear a moment after release-please has pushed the branch.
	runs=""
	for ((try = 1; try <= TRIES; try++)); do
		runs="$(runs_on "$sha")"
		if [ -n "$runs" ]; then
			break
		fi
		if [ "$try" -lt "$TRIES" ]; then
			sleep "$WAIT"
		fi
	done
	if [ -z "$runs" ]; then
		echo "error: no pull request run on $branch at $sha after $TRIES tries" >&2
		exit 1
	fi
	while read -r id conclusion run_branch run_repo; do
		if [[ ! "$id" =~ ^[0-9]+$ ]]; then
			echo "error: unexpected run on $sha: $id" >&2
			exit 1
		fi
		# Only a run of this repository's own release branch is approved.
		if [ "$conclusion" != action_required ] || [ "$run_branch" != "$branch" ] || [ "$run_repo" != "$REPO" ]; then
			continue
		fi
		"$GH" api --method POST "repos/$REPO/actions/runs/$id/approve" > /dev/null
		echo "Approved run $id on $branch at $sha."
	done <<< "$runs"
done <<< "$HEADS"
