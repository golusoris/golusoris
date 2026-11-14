#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

main() {
	if [[ "${1:-}" != --pushed ]]; then
		printf 'usage: %s --pushed [PATH...]\n' "${0##*/}" >&2
		return 2
	fi
	shift
	if (( $# > 4096 )); then
		printf 'python-tests: pushed path count exceeds 4096\n' >&2
		return 2
	fi
	local path root selected=0
	local -a suites=()
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)" || return $?
	for path in "$@"; do
		path="${path//\\//}"
		case "$path" in
			scripts/ci/*.py | .config/lefthook/scripts/*.py)
				if ! git -C "$root" diff --quiet HEAD -- "$path"; then
					printf 'python-tests: %s has uncommitted changes; commit them before pushing\n' "$path" >&2
					return 1
				fi
				selected=1
				;;
		esac
		case "$path" in
			scripts/ci/*_test.py | .config/lefthook/scripts/test_*.py) suites+=(--suite "$path") ;;
		esac
	done
	if (( selected )); then
		env -u CI_SCOPE_BASE bash "$root/scripts/ci/python-tests.sh" --root "$root" "${suites[@]}" || return $?
		return 0
	fi
	printf 'python-tests: no Python policy files pushed, skipping\n'
}

main "$@"
