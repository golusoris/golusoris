#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

usage() {
	printf 'usage: %s [--root PATH] [--suite REL_PATH]...\n' "${0##*/}" >&2
}

# Refuse arbitrary paths before they can become executable inputs.
validate_suite() {
	local root="$1" path="$2"
	case "$path" in
		scripts/ci/*_test.py | .config/lefthook/scripts/test_*.py) ;;
		*) printf 'Python tests refuse suite path: %s\n' "$path" >&2; return 1 ;;
	esac
	if [[ "$path" == *..* || -L "$root/$path" ]]; then
		printf 'Python tests refuse symlink input or traversal: %s\n' "$path" >&2
		return 1
	fi
	if [[ ! -f "$root/$path" ]]; then
		printf 'required Python test is missing: %s\n' "$path" >&2
		return 1
	fi
}

# Locate an executable Python >= 3.12 interpreter required by repository test suites.
find_python() {
	local python_bin="${PYTHON_BIN:-}"
	if [[ -z "$python_bin" ]] && command -v python3 >/dev/null; then
		python_bin="$(command -v python3)"
	fi
	if [[ -n "$python_bin" && -x "$python_bin" ]] && "$python_bin" -B -c \
		'import sys; raise SystemExit(0 if sys.version_info >= (3, 12) else 1)'; then
		printf '%s\n' "$python_bin"
		return
	fi
	printf 'Python 3.12 or newer is required for repository Python tests\n' >&2
	return 1
}

# Print core.bare of the repository at $1; empty when unset.
core_bare() {
	local value rc=0
	value="$(git -C "$1" config --get core.bare)" || rc=$?
	case "$rc" in
		0 | 1) printf '%s\n' "$value" ;;
		*) return "$rc" ;;
	esac
}

# Run one suite from root; fail when it rewrote core.bare of the repository under test.
run_suite() {
	local root="$1" python_bin="$2" test_file="$3" bare_before="$4" bare_after rc=0
	(cd "$root" && PYTHONDONTWRITEBYTECODE=1 "$python_bin" -B "$test_file") || rc=$?
	bare_after="$(core_bare "$root")" || return 1
	if [[ "$bare_after" != "$bare_before" ]]; then
		printf 'python-tests: %s changed core.bare from %q to %q; restore it with git config core.bare %q\n' \
			"$test_file" "$bare_before" "$bare_after" "${bare_before:-false}" >&2
		if ! restore_core_bare "$root" "$bare_before"; then
			printf 'python-tests: could not restore core.bare to %q\n' "$bare_before" >&2
		fi
		return 1
	fi
	return "$rc"
}

restore_core_bare() {
	if [[ -n "$2" ]]; then
		git -C "$1" config core.bare "$2"
	else
		git -C "$1" config --unset core.bare
	fi
}

# Explicit suites are validated before scope selection so bad inputs never disappear.
main() {
	local root names path
	local -a local_vars suites=()
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if (( $# > 132 )); then usage; return 2; fi
	while (( $# > 0 )); do
		if (( $# < 2 )); then usage; return 2; fi
		case "$1" in
			--root) root="$(cd "$2" && pwd)" ;;
			--suite) suites+=("$2") ;;
			*) usage; return 2 ;;
		esac
		shift 2
	done
	if (( ${#suites[@]} > 64 )); then usage; return 2; fi
	# A git hook exports GIT_DIR; inherited, it points every fixture git init at this repository.
	names="$(git rev-parse --local-env-vars)"
	mapfile -t local_vars <<<"$names"
	unset "${local_vars[@]}"
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null
	for path in "${suites[@]}"; do
		validate_suite "$root" "$path" || return 1
	done
	local python_bin
	python_bin="$(find_python)"
	run_tests "$root" "$python_bin" "${suites[@]}"
}

# Scope and run required repository Python test suites against the worktree.
run_tests() {
	local root="$1" python_bin="$2"
	shift 2
	if [[ -f "$root/scripts/ci/ci_scope.py" ]]; then
		local rc=0
		"$python_bin" "$root/scripts/ci/ci_scope.py" --root "$root" any '*.py' 'scripts/ci/*' '.config/lefthook/*' '*AGENTS.md' || rc=$?
		case "$rc" in
			0) ;;
			1)
				printf 'ci-scope: no Python test inputs changed; skipping python-tests\n'
				return 0
				;;
			*) return 1 ;;
		esac
	fi

	local -a tests=(
		'.config/lefthook/scripts/test_checkpoint.py'
		'scripts/ci/block_evasion_hook_test.py'
		'scripts/ci/portability_test.py'
		'scripts/ci/ci_scope_test.py'
		'scripts/ci/gomod_test.py'
		'scripts/ci/pull_test_images_test.py'
		'scripts/ci/renovate_vulnerability_test.py'
		'scripts/ci/repo_git_test.py'
		'scripts/ci/review_bot_badges_test.py'
		'scripts/ci/context_size_test.py'
		'scripts/ci/tiny-litert-sidecar-docs-policy_test.py'
	)
	local test_file bare_before
	local -A seen=()
	bare_before="$(core_bare "$root")"
	for test_file in "${tests[@]}" "$@"; do
		if [[ -n "${seen[$test_file]:-}" ]]; then continue; fi
		seen["$test_file"]=1
		validate_suite "$root" "$test_file" || return 1
		run_suite "$root" "$python_bin" "$test_file" "$bare_before" || return $?
	done
	printf 'Python tests: %d suites passed\n' "${#seen[@]}"
}

main "$@"
