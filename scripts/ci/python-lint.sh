#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly max_python_files=4096

usage() {
	printf 'usage: %s [--root PATH]\n' "${0##*/}" >&2
}

resolve_root() {
	local root
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if [[ $# -eq 0 ]]; then
		printf '%s\n' "$root"
		return
	fi
	if [[ $# -ne 2 || "$1" != --root ]]; then
		usage
		return 2
	fi
	(cd "$2" && pwd)
}

collect_files() {
	local root="$1"
	local path
	while IFS= read -r -d '' path; do
		case "$path" in
			.config/hiss/testdata/* | vendor/* | third_party/*)
				continue
			;;
		esac
		[[ -f "$root/$path" || -L "$root/$path" ]] || continue
		if [[ -L "$root/$path" ]]; then
			printf 'Ruff refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		files+=("$path")
		if (( ${#files[@]} > max_python_files )); then
			printf 'Ruff file bound exceeded: %d > %d\n' \
				"${#files[@]}" "$max_python_files" >&2
			return 1
		fi
	done < <(git -C "$root" ls-files -z --cached --others --exclude-standard -- '*.py')
}

check_noqa_explanations() {
	local root="$1"
	shift
	local path line
	local line_number
	local explained_noqa='#[[:space:]]*noqa:[[:space:]]*[A-Z][A-Z0-9]*(,[[:space:]]*[A-Z][A-Z0-9]*)*[[:space:]]+-[[:space:]]+[^[:space:]]'
	for path in "$@"; do
		line_number=0
		while IFS= read -r line || [[ -n "$line" ]]; do
			((line_number += 1))
			if [[ "$line" == *'# ruff: noqa'* ]]; then
				printf 'Ruff file-level noqa is forbidden; use a scoped explained suppression: %s:%d\n' \
					"$path" "$line_number" >&2
				return 1
			fi
			if [[ "$line" == *'# noqa'* ]] &&
				[[ ! "$line" =~ $explained_noqa ]]; then
				printf 'Ruff noqa needs specific codes and an inline WHY: %s:%d\n' \
					"$path" "$line_number" >&2
				return 1
			fi
		done <"$root/$path"
	done
}

main() {
	local root
	root="$(resolve_root "$@")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null

	# shellcheck source=/dev/null
	. "$root/tools/tool-versions.env"
	local docker_bin="${DOCKER_BIN:-}"
	if [[ -z "$docker_bin" ]] && command -v docker >/dev/null; then
		docker_bin="$(command -v docker)"
	fi
	if [[ -z "$docker_bin" || ! -x "$docker_bin" ]]; then
		printf 'Docker is required for digest-pinned Ruff %s\n' "$RUFF_VERSION" >&2
		return 1
	fi

	local -a files=()
	collect_files "$root"
	if (( ${#files[@]} == 0 )); then
		printf 'Ruff selected no repository Python files\n' >&2
		return 1
	fi
	check_noqa_explanations "$root" "${files[@]}"

	local image="ghcr.io/astral-sh/ruff:${RUFF_VERSION}@${RUFF_IMAGE_DIGEST}"
	"$docker_bin" run --rm \
		--network none \
		--read-only \
		--cap-drop ALL \
		--security-opt no-new-privileges \
		-u "$(id -u):$(id -g)" \
		--tmpfs /tmp:rw,noexec,nosuid,size=32m \
		-v "$root:/repo:ro" \
		-w /repo \
		"$image" check \
		--isolated \
		--no-cache \
		--target-version py312 \
		--select E4,E7,E9,F,I,B,UP,S,RUF,PERF,ASYNC,C90 \
		--output-format concise \
		-- "${files[@]}"
	printf 'Ruff: %d repository Python files passed\n' "${#files[@]}"
}

main "$@"
