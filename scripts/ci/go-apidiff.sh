#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
repo_root="$(cd "$script_dir/../.." && pwd)"
readonly repo_root
readonly modules_script="$script_dir/go-modules.sh"
readonly apidiff_bin="${GO_APIDIFF_BIN:-go-apidiff}"
readonly enforce_incompatible="${GO_APIDIFF_ENFORCE_INCOMPATIBLE:-false}"

usage() {
	printf 'usage: %s <old-revision> [new-revision]\n' "$0" >&2
}

load_modules() {
	local ref="$1"
	local output
	if ! output="$("$modules_script" list-ref "$ref")"; then
		printf 'failed to discover Go modules at %s\n' "$ref" >&2
		return 1
	fi
	printf '%s\n' "$output"
}

compare_module() {
	local module="$1"
	local old_commit="$2"
	local new_commit="$3"
	local status

	printf '==> %s: API compatibility\n' "$module"
	if (
		cd "$repo_root/$module"
		"$apidiff_bin" --repo-path="$repo_root" "$old_commit" "$new_commit" \
			--print-compatible
	); then
		status=0
	else
		status=$?
	fi
	case "$status" in
	0) return 0 ;;
	1)
		printf '::warning::%s contains pre-1.0 incompatible API changes\n' "$module" >&2
		return 1
		;;
	*)
		printf '::error::go-apidiff failed for %s with status %d\n' "$module" "$status" >&2
		return "$status"
		;;
	esac
}

preflight() {
	if (($# < 1 || $# > 2)); then
		usage
		return 2
	fi
	case "$enforce_incompatible" in
	true | false) ;;
	*)
		printf 'GO_APIDIFF_ENFORCE_INCOMPATIBLE must be true or false\n' >&2
		return 2
		;;
	esac
	if ! command -v "$apidiff_bin" >/dev/null 2>&1; then
		printf 'go-apidiff binary not found: %s\n' "$apidiff_bin" >&2
		return 2
	fi
	if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
		printf 'go-apidiff requires a clean repository worktree\n' >&2
		return 2
	fi
}

# compare_new_modules diffs each module in main's new_modules that old_set also holds; it updates
# main's checked, added and incompatible counters.
compare_new_modules() {
	local old_commit="$1"
	local new_commit="$2"
	local module status
	for module in "${new_modules[@]}"; do
		if [[ -z "${old_set[$module]+present}" ]]; then
			printf '==> %s: new module; no previous API exists\n' "$module"
			added=$((added + 1))
			continue
		fi
		if compare_module "$module" "$old_commit" "$new_commit"; then
			status=0
		else
			status=$?
		fi
		case "$status" in
		0) ;;
		1) incompatible=$((incompatible + 1)) ;;
		*) return "$status" ;;
		esac
		checked=$((checked + 1))
	done
}

# count_removed_modules warns for each module in main's old_modules that new_set lacks; it updates
# main's removed and incompatible counters.
count_removed_modules() {
	local old_ref="$1"
	local module
	for module in "${old_modules[@]}"; do
		if [[ -n "${new_set[$module]+present}" ]]; then
			continue
		fi
		printf '::warning::Go module removed since %s: %s\n' "$old_ref" "$module" >&2
		removed=$((removed + 1))
		incompatible=$((incompatible + 1))
	done
}

main() {
	preflight "$@" || return

	local old_ref="$1"
	local new_ref="${2:-HEAD}"
	local old_commit new_commit head_commit old_output new_output module
	local checked=0 added=0 removed=0 incompatible=0
	local -a old_modules=() new_modules=()
	local -A old_set=() new_set=()

	old_commit="$(git -C "$repo_root" rev-parse --verify --end-of-options "$old_ref^{commit}")"
	new_commit="$(git -C "$repo_root" rev-parse --verify --end-of-options "$new_ref^{commit}")"
	head_commit="$(git -C "$repo_root" rev-parse --verify HEAD)"
	if [[ "$new_commit" != "$head_commit" ]]; then
		printf 'new revision must match checked-out HEAD: %s != %s\n' "$new_commit" "$head_commit" >&2
		return 2
	fi

	old_output="$(load_modules "$old_commit")"
	new_output="$(load_modules "$new_commit")"
	mapfile -t old_modules <<<"$old_output"
	mapfile -t new_modules <<<"$new_output"
	for module in "${old_modules[@]}"; do
		old_set["$module"]=1
	done
	for module in "${new_modules[@]}"; do
		new_set["$module"]=1
	done

	compare_new_modules "$old_commit" "$new_commit" || return
	count_removed_modules "$old_ref"

	printf 'API module coverage: checked=%d added=%d removed=%d incompatible=%d current=%d\n' \
		"$checked" "$added" "$removed" "$incompatible" "${#new_modules[@]}"
	if ((incompatible > 0)) && [[ "$enforce_incompatible" == true ]]; then
		return 1
	fi
}

main "$@"
