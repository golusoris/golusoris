#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

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

main() {
	local root
	root="$(resolve_root "$@")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null

	local python_bin="${PYTHON_BIN:-}"
	if [[ -z "$python_bin" ]] && command -v python3 >/dev/null; then
		python_bin="$(command -v python3)"
	fi
	if [[ -z "$python_bin" || ! -x "$python_bin" ]]; then
		printf 'Python 3.12 or newer is required for repository Python tests\n' >&2
		return 1
	fi
	if ! "$python_bin" -B -c \
		'import sys; raise SystemExit(0 if sys.version_info >= (3, 12) else 1)'; then
		printf 'Python 3.12 or newer is required for repository Python tests\n' >&2
		return 1
	fi

	local -a tests=(
		'.config/lefthook/scripts/test_checkpoint.py'
		'scripts/ci/block_evasion_hook_test.py'
		'scripts/ci/portability_test.py'
	)
	local test_file
	for test_file in "${tests[@]}"; do
		if [[ -L "$root/$test_file" ]]; then
			printf 'Python tests refuse symlink input: %s\n' "$test_file" >&2
			return 1
		fi
		if [[ ! -f "$root/$test_file" ]]; then
			printf 'required Python test is missing: %s\n' "$test_file" >&2
			return 1
		fi
		(
			cd "$root"
			PYTHONDONTWRITEBYTECODE=1 "$python_bin" -B "$test_file"
		)
	done
	printf 'Python tests: %d required suites passed\n' "${#tests[@]}"
}

main "$@"
