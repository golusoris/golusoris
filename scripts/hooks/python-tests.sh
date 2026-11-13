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
	local path root
	for path in "$@"; do
		case "${path//\\//}" in
			scripts/ci/*.py | .config/lefthook/scripts/*.py)
				root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)" || return $?
				bash "$root/scripts/ci/python-tests.sh" --root "$root" || return $?
				return 0
				;;
		esac
	done
	printf 'python-tests: no Python policy files pushed, skipping\n'
}

main "$@"
