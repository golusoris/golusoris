#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

# Keep invalid invocations from selecting an unintended repository.
usage() {
	printf 'usage: %s [--root PATH]\n' "${0##*/}" >&2
}

# Glob order stays stable across locales and includes suites outside the fixed runner list.
run_suites() {
	local root="$1" base="$2" physical="$3" path rc=0
	local LC_ALL=C
	local -a paths suites=()
	shopt -s nullglob
	paths=("$root"/scripts/ci/*_test.py)
	if (( ${#paths[@]} == 0 )); then
		printf 'python-symlink-tmpdir: no scripts/ci/*_test.py suites under %s\n' "$root" >&2
		return 1
	fi
	for path in "${paths[@]}"; do
		suites+=(--suite "${path#"$root"/}")
	done
	TMPDIR="$base/link" bash "$root/scripts/ci/python-tests.sh" --root "$root" "${suites[@]}" || rc=$?
	if (( rc != 0 )); then
		printf 'python-symlink-tmpdir: TMPDIR=%s -> %s; compare paths against Path.resolve() (macOS /private, Windows 8.3 names)\n' \
			"$base/link" "$physical" >&2
	fi
	return "$rc"
}

# A subshell keeps the EXIT cleanup bound to this invocation's temporary root.
main() (
	local root platform base physical linked
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if (( $# != 0 )); then
		if [[ $# -ne 2 || "$1" != --root ]]; then usage; return 2; fi
		root="$(cd "$2" && pwd)"
	fi
	platform="$(uname -s)"
	case "$platform" in
		MINGW* | MSYS* | CYGWIN*)
			printf 'python-symlink-tmpdir: skip: %s needs symlink privilege; Windows 8.3 short names stay CI-only (Platform Neutrality matrix)\n' "$platform"
			return 0
			;;
	esac
	base="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-symlink-tmpdir.XXXXXX")"
	trap 'find "$base" -depth -delete' EXIT
	mkdir "$base/real"
	ln -s real "$base/link"
	physical="$(cd "$base/real" && pwd -P)"
	linked="$(cd "$base/link" && pwd -P)"
	if [[ ! -L "$base/link" || "$linked" != "$physical" || "$linked" == "$base/link" ]]; then
		printf 'python-symlink-tmpdir: invalid symlink: %s -> %s (expected %s)\n' "$base/link" "$linked" "$physical" >&2
		return 1
	fi
	run_suites "$root" "$base" "$physical"
)

main "$@"
