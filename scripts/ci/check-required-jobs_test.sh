#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly POLICY="$SCRIPT_DIR/check-required-jobs.sh"

run_policy() {
	env \
		PR_TITLE="${PR_TITLE:-success}" \
		LINT="${LINT:-success}" \
		GOSEC="${GOSEC:-success}" \
		VULN="${VULN:-success}" \
		TEST="${TEST:-success}" \
		BUILD="${BUILD:-success}" \
		MODULE_SWEEP="${MODULE_SWEEP:-success}" \
		MKDOCS="${MKDOCS:-success}" \
		ALLOCATION_BUDGET="${ALLOCATION_BUDGET:-success}" \
		CORE_SKEW="${CORE_SKEW:-success}" \
		FAULT_RECOVERY="${FAULT_RECOVERY:-success}" \
		SHELLCHECK="${SHELLCHECK:-success}" \
		ACTIONLINT="${ACTIONLINT:-success}" \
		CI_POLICY="${CI_POLICY:-success}" \
		TERRAFORM="${TERRAFORM:-success}" \
		KUBECONFORM="${KUBECONFORM:-success}" \
		SEMGREP="${SEMGREP:-success}" \
		SPECTRAL="${SPECTRAL:-success}" \
		DEPENDENCY_REVIEW="${DEPENDENCY_REVIEW:-success}" \
		GITLEAKS="${GITLEAKS:-success}" \
		DCO="${DCO:-success}" \
		ATTRIBUTION="${ATTRIBUTION:-skipped}" \
		REUSE="${REUSE:-success}" \
		APIDIFF="${APIDIFF:-success}" \
		APP_IMAGE_SMOKE="${APP_IMAGE_SMOKE:-success}" \
		EVENT_NAME="${EVENT_NAME:-pull_request}" \
		ACTOR="${ACTOR:-contributor}" \
		bash "$POLICY"
}

expect_failure() {
	if "$@" >/dev/null 2>&1; then
		printf 'expected failure: %s\n' "$*" >&2
		exit 1
	fi
}

if [[ "${1:-}" == --single ]]; then
	run_policy >/dev/null
	exit 0
fi

run_policy >/dev/null
ACTOR='renovate[bot]' DCO=skipped PR_TITLE=skipped run_policy >/dev/null
ACTOR='github-actions[bot]' DCO=skipped run_policy >/dev/null
EVENT_NAME=push DEPENDENCY_REVIEW=skipped DCO=skipped PR_TITLE=skipped run_policy >/dev/null
EVENT_NAME=merge_group DEPENDENCY_REVIEW=skipped DCO=skipped ATTRIBUTION=success PR_TITLE=skipped run_policy >/dev/null
expect_failure env LINT=skipped bash "$0" --single
expect_failure env MKDOCS=skipped bash "$0" --single
expect_failure env ALLOCATION_BUDGET=failure bash "$0" --single
expect_failure env CORE_SKEW=failure bash "$0" --single
expect_failure env CORE_SKEW=skipped bash "$0" --single
expect_failure env FAULT_RECOVERY=failure bash "$0" --single
expect_failure env FAULT_RECOVERY=skipped bash "$0" --single
expect_failure env SHELLCHECK=failure bash "$0" --single
expect_failure env ACTIONLINT=skipped bash "$0" --single
expect_failure env CI_POLICY=failure bash "$0" --single
expect_failure env CI_POLICY=skipped bash "$0" --single
expect_failure env TERRAFORM=failure bash "$0" --single
expect_failure env KUBECONFORM=skipped bash "$0" --single
expect_failure env SEMGREP=failure bash "$0" --single
expect_failure env GITLEAKS=failure bash "$0" --single
expect_failure env APIDIFF=failure bash "$0" --single
expect_failure env APP_IMAGE_SMOKE=failure bash "$0" --single
expect_failure env APP_IMAGE_SMOKE=skipped bash "$0" --single
expect_failure env DEPENDENCY_REVIEW=skipped bash "$0" --single
expect_failure env PR_TITLE=skipped EVENT_NAME=push DEPENDENCY_REVIEW=success DCO=skipped bash "$0" --single
expect_failure env PR_TITLE=skipped EVENT_NAME=push DEPENDENCY_REVIEW=skipped DCO=success bash "$0" --single
expect_failure env ACTOR='renovate[bot]' PR_TITLE=skipped DCO=success bash "$0" --single
expect_failure env ACTOR='github-actions[bot]' DCO=success bash "$0" --single
expect_failure env DCO=skipped bash "$0" --single
expect_failure env PR_TITLE=failure bash "$0" --single
expect_failure env PR_TITLE=skipped bash "$0" --single
expect_failure env ACTOR='github-actions[bot]' DCO=skipped PR_TITLE=skipped bash "$0" --single
expect_failure env ACTOR='renovate[bot]' DCO=skipped PR_TITLE=success bash "$0" --single
expect_failure env PR_TITLE=success EVENT_NAME=merge_group DEPENDENCY_REVIEW=skipped DCO=skipped ATTRIBUTION=success bash "$0" --single
expect_failure env PR_TITLE=skipped EVENT_NAME=merge_group TEST=skipped DEPENDENCY_REVIEW=skipped DCO=skipped ATTRIBUTION=success bash "$0" --single
expect_failure env PR_TITLE=skipped EVENT_NAME=merge_group DEPENDENCY_REVIEW=skipped DCO=success ATTRIBUTION=success bash "$0" --single
expect_failure env PR_TITLE=skipped EVENT_NAME=merge_group DEPENDENCY_REVIEW=skipped DCO=skipped ATTRIBUTION=skipped bash "$0" --single
expect_failure env PR_TITLE=skipped EVENT_NAME=merge_group DEPENDENCY_REVIEW=skipped DCO=skipped ATTRIBUTION=failure bash "$0" --single
expect_failure env ATTRIBUTION=success bash "$0" --single
expect_failure env PR_TITLE=skipped EVENT_NAME=push DEPENDENCY_REVIEW=skipped DCO=skipped ATTRIBUTION=success bash "$0" --single

printf 'required-job result policy tests passed\n'
