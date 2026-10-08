#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly POLICY="$SCRIPT_DIR/check-required-jobs.sh"

run_policy() {
	env \
		LINT="${LINT:-success}" \
		GOSEC="${GOSEC:-success}" \
		VULN="${VULN:-success}" \
		TEST="${TEST:-success}" \
		BUILD="${BUILD:-success}" \
		MODULE_SWEEP="${MODULE_SWEEP:-success}" \
		MKDOCS="${MKDOCS:-success}" \
		ALLOCATION_BUDGET="${ALLOCATION_BUDGET:-success}" \
		SHELLCHECK="${SHELLCHECK:-success}" \
		ACTIONLINT="${ACTIONLINT:-success}" \
		TERRAFORM="${TERRAFORM:-success}" \
		KUBECONFORM="${KUBECONFORM:-success}" \
		SEMGREP="${SEMGREP:-success}" \
		SPECTRAL="${SPECTRAL:-success}" \
		DEPENDENCY_REVIEW="${DEPENDENCY_REVIEW:-success}" \
		GITLEAKS="${GITLEAKS:-success}" \
		DCO="${DCO:-success}" \
		REUSE="${REUSE:-success}" \
		APIDIFF="${APIDIFF:-success}" \
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
ACTOR='renovate[bot]' DCO=skipped run_policy >/dev/null
EVENT_NAME=push DEPENDENCY_REVIEW=skipped DCO=skipped run_policy >/dev/null
expect_failure env LINT=skipped bash "$0" --single
expect_failure env MKDOCS=skipped bash "$0" --single
expect_failure env ALLOCATION_BUDGET=failure bash "$0" --single
expect_failure env SHELLCHECK=failure bash "$0" --single
expect_failure env ACTIONLINT=skipped bash "$0" --single
expect_failure env TERRAFORM=failure bash "$0" --single
expect_failure env KUBECONFORM=skipped bash "$0" --single
expect_failure env SEMGREP=failure bash "$0" --single
expect_failure env GITLEAKS=failure bash "$0" --single
expect_failure env APIDIFF=failure bash "$0" --single
expect_failure env DEPENDENCY_REVIEW=skipped bash "$0" --single
expect_failure env EVENT_NAME=push DEPENDENCY_REVIEW=success DCO=skipped bash "$0" --single
expect_failure env EVENT_NAME=push DEPENDENCY_REVIEW=skipped DCO=success bash "$0" --single
expect_failure env ACTOR='renovate[bot]' DCO=success bash "$0" --single
expect_failure env DCO=skipped bash "$0" --single

printf 'required-job result policy tests passed\n'
