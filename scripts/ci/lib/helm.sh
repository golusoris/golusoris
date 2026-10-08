# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# Pinned-helm helpers for scripts/ci/*.sh (sourced, never executed; callers set strict mode).
# shellcheck shell=bash

helm_module_version() {
	local metadata
	metadata="$(go version -m "$1" 2>/dev/null)" || return
	awk '$1 == "mod" && $2 == "helm.sh/helm/v4" { version = $3 } END { print version }' \
		<<<"$metadata"
}

resolve_pinned_helm() {
	local expected_version="$1"
	local selected="${HELM_BIN:-}"
	local go_bin go_path ambient_helm candidate actual_version
	if [[ -z "$selected" ]]; then
		go_bin="$(go env GOBIN)"
		go_path="$(go env GOPATH)"
		go_path="${go_path%%:*}"
		ambient_helm=""
		if command -v helm >/dev/null; then
			ambient_helm="$(command -v helm)"
		fi
		for candidate in "${go_bin:-$go_path/bin}/helm" "$ambient_helm"; do
			if [[ -n "$candidate" && -x "$candidate" ]] &&
				[[ "$(helm_module_version "$candidate")" == "$expected_version" ]]; then
				selected="$candidate"
				break
			fi
		done
	fi
	if [[ -z "$selected" || ! -x "$selected" ]]; then
		printf 'pinned helm %s is required; run make tools-bootstrap or set HELM_BIN\n' \
			"$expected_version" >&2
		return 1
	fi
	actual_version="$(helm_module_version "$selected")"
	if [[ "$actual_version" != "$expected_version" ]]; then
		printf 'helm module version is %s, want %s\n' \
			"${actual_version:-unknown}" "$expected_version" >&2
		return 1
	fi
	printf '%s\n' "$selected"
}
