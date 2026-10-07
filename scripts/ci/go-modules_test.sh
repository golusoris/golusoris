#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

TEST_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly TEST_SCRIPT_DIR

# Resolved from this test's directory rather than the caller's working tree.
# shellcheck disable=SC1091 # Source path is resolved from this test file at runtime.
source "$TEST_SCRIPT_DIR/go-modules.sh"

head_modules="$(discover_modules_at_ref HEAD)"
grep -Fqx '.' <<<"$head_modules"
grep -Fqx 'core' <<<"$head_modules"
grep -Fqx 'web3/solana' <<<"$head_modules"

worktree_modules="$(discover_modules)"
[[ "$(wc -l <<<"$worktree_modules")" -eq 23 ]]
grep -Fqx 'deploy' <<<"$worktree_modules"
if grep -Eq '^deploy/(multiregion|pulumi)$' <<<"$worktree_modules"; then
	printf 'deployment reference programs must share deploy/go.mod\n' >&2
	exit 1
fi
if discover_modules_at_ref refs/heads/does-not-exist >/dev/null 2>&1; then
	printf 'discover_modules_at_ref accepted a missing revision\n' >&2
	exit 1
fi

primary_profiles=()
record_primary_coverage_profile "." "/tmp/root.out"
record_primary_coverage_profile "container/registry" "/tmp/container-registry.out"
record_primary_coverage_profile "core" "/tmp/core.out"

[[ "${primary_profiles[*]}" == "/tmp/root.out /tmp/core.out" ]]

if (
	# Invoked indirectly when select_modules resolves its dependency.
	# shellcheck disable=SC2329 # select_modules invokes this injected failure seam indirectly.
	discover_modules() {
		printf '.\ncore\n'
		return 23
	}
	select_modules
); then
	printf 'select_modules accepted partial discovery output\n' >&2
	exit 1
fi

if (
	# Invoked indirectly when main resolves its dependency.
	# shellcheck disable=SC2329 # main invokes this injected failure seam indirectly.
	select_modules() {
		printf '.\ncore\n'
		return 23
	}
	main list
); then
	printf 'main accepted partial module selection output\n' >&2
	exit 1
fi

if (
	# Invoked indirectly when build_module lists packages.
	# shellcheck disable=SC2329 # build_module invokes this injected Go seam indirectly.
	go() {
		if [[ "${1:-}" == "list" ]]; then
			printf 'github.com/golusoris/golusoris\n'
			return 23
		fi
		command go "$@"
	}
	build_module .
); then
	printf 'build_module accepted partial package-list output\n' >&2
	exit 1
fi

printf 'go-module coverage selection tests passed\n'
