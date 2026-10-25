#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Waits until SHA carries a completed CHECK_NAME check run and exits 0 only if it
# succeeded. The aggregate check is created when all of its needs finish, so an
# absent check is a wait while the CI workflow run for SHA is still going.
#   usage: SHA=... CHECK_NAME=... GITHUB_REPOSITORY=owner/repo scripts/ci/require-green-ci.sh
set -euo pipefail

: "${SHA:?SHA is required}"
: "${CHECK_NAME:?CHECK_NAME is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
CI_WORKFLOW="${CI_WORKFLOW:-ci.yml}"

# Check names carry +, & and # ("Test (race + coverage)"), so URI-encode the query value.
CHECK_NAME_ENCODED=$(jq -rn --arg v "$CHECK_NAME" '$v|@uri')

# HISS-02: scalar bounds on the wait. Main's CI is slow on cold runners (Lint
# alone is allowed 65 min), hence the generous ceiling. Finite, though: a tag
# that cannot show a green CI is not a release.
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-30}"
POLL_ATTEMPTS="${POLL_ATTEMPTS:-180}" # 180 x 30 s = 90 min
# A pushed commit gets its CI workflow run within seconds, but a busy queue can
# delay that. Tolerate no run at all only this long; past it the commit is unverified.
MISSING_GRACE_ATTEMPTS="${MISSING_GRACE_ATTEMPTS:-20}" # 10 min

check_state() {
	# A re-run attaches a second check run with the same name; the newest has the
	# highest id (a queued one has no started_at yet).
	gh api \
		"repos/${GITHUB_REPOSITORY}/commits/${SHA}/check-runs?check_name=${CHECK_NAME_ENCODED}&per_page=100" \
		--jq '[.check_runs[]] | sort_by(.id) | last
		      | if . == null then "missing none"
		        else "\(.status) \(.conclusion // "none")" end'
}

ci_run_status() {
	gh api \
		"repos/${GITHUB_REPOSITORY}/actions/workflows/${CI_WORKFLOW}/runs?head_sha=${SHA}&per_page=100" \
		--jq '[.workflow_runs[]] | sort_by(.created_at) | last
		      | if . == null then "none" else .status end'
}

echo "Gating ${SHA} on check \"${CHECK_NAME}\" of workflow ${CI_WORKFLOW}"
for ((attempt = 1; attempt <= POLL_ATTEMPTS; attempt++)); do
	state=$(check_state)
	status=${state%% *}
	conclusion=${state##* }

	case "$status" in
	completed)
		if [ "$conclusion" = "success" ]; then
			echo "\"${CHECK_NAME}\" passed on ${SHA}."
			exit 0
		fi
		run=$(ci_run_status)
		case "$run" in
		queued|in_progress|waiting|pending|requested)
			echo "attempt ${attempt}/${POLL_ATTEMPTS}: \"${CHECK_NAME}\" concluded ${conclusion} but ${CI_WORKFLOW} run is ${run} (re-run in progress); waiting ${POLL_INTERVAL_SECONDS}s"
			;;
		*)
			echo "::error::\"${CHECK_NAME}\" concluded ${conclusion} on ${SHA} — refusing to publish."
			exit 1
			;;
		esac
		;;
	missing)
		run=$(ci_run_status)
		case "$run" in
		none)
			if [ "$attempt" -gt "$MISSING_GRACE_ATTEMPTS" ]; then
				echo "::error::no ${CI_WORKFLOW} run and no \"${CHECK_NAME}\" check run on ${SHA} — refusing to publish an unverified commit."
				exit 1
			fi
			echo "attempt ${attempt}/${POLL_ATTEMPTS}: no ${CI_WORKFLOW} run yet; waiting ${POLL_INTERVAL_SECONDS}s"
			;;
		completed)
			echo "::error::${CI_WORKFLOW} run on ${SHA} completed without a \"${CHECK_NAME}\" check run — refusing to publish."
			exit 1
			;;
		*)
			echo "attempt ${attempt}/${POLL_ATTEMPTS}: ${CI_WORKFLOW} run is ${run}, \"${CHECK_NAME}\" not created yet; waiting ${POLL_INTERVAL_SECONDS}s"
			;;
		esac
		;;
	*)
		echo "attempt ${attempt}/${POLL_ATTEMPTS}: \"${CHECK_NAME}\" is ${status}; waiting ${POLL_INTERVAL_SECONDS}s"
		;;
	esac
	sleep "$POLL_INTERVAL_SECONDS"
done

echo "::error::\"${CHECK_NAME}\" did not conclude on ${SHA} within $((POLL_ATTEMPTS * POLL_INTERVAL_SECONDS / 60)) min — refusing to publish."
exit 1
