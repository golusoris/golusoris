#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

require_success() {
	local name="$1"
	local result="$2"
	if [[ "$result" != success ]]; then
		printf '::error::%s must succeed; result=%s\n' "$name" "$result" >&2
		return 1
	fi
}

require_conditional() {
	local name="$1"
	local result="$2"
	local applicable="$3"
	if [[ "$applicable" == true ]]; then
		require_success "$name" "$result"
	elif [[ "$result" != skipped ]]; then
		printf '::error::%s must be skipped when not applicable; result=%s\n' \
			"$name" "$result" >&2
		return 1
	fi
}

main() {
	require_success lint "${LINT:?}"
	require_success gosec "${GOSEC:?}"
	require_success govulncheck "${VULN:?}"
	require_success test "${TEST:?}"
	require_success build "${BUILD:?}"
	require_success module-sweep "${MODULE_SWEEP:?}"
	require_success mkdocs "${MKDOCS:?}"
	require_success allocation-budget "${ALLOCATION_BUDGET:?}"
	require_success shellcheck "${SHELLCHECK:?}"
	require_success actionlint "${ACTIONLINT:?}"
	require_success policy "${CI_POLICY:?}"
	require_success terraform "${TERRAFORM:?}"
	require_success kubeconform "${KUBECONFORM:?}"
	require_success semgrep "${SEMGREP:?}"
	require_success spectral "${SPECTRAL:?}"
	require_success gitleaks "${GITLEAKS:?}"
	require_success reuse "${REUSE:?}"
	require_success apidiff "${APIDIFF:?}"

	local is_pr=false
	[[ "${EVENT_NAME:?}" == pull_request ]] && is_pr=true
	require_conditional dependency-review "${DEPENDENCY_REVIEW:?}" "$is_pr"

	local dco_applicable="$is_pr"
	# Bots author no human contribution to certify: Renovate and the release-please bot.
	[[ "${ACTOR:?}" == 'renovate[bot]' || "${ACTOR:?}" == 'github-actions[bot]' ]] && dco_applicable=false
	require_conditional dco "${DCO:?}" "$dco_applicable"

	printf 'all gating jobs passed\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
