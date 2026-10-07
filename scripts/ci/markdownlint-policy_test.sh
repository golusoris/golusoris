#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d)"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools/markdownlint" "$root/docs"
	install -m 0755 "$repo_root/scripts/ci/markdownlint.sh" "$root/scripts/ci/markdownlint.sh"
	install -m 0644 "$repo_root/.markdownlint-cli2.jsonc" "$root/.markdownlint-cli2.jsonc"
	install -m 0644 "$repo_root/tools/markdownlint/package.json" \
		"$root/tools/markdownlint/package.json"
	install -m 0644 "$repo_root/tools/markdownlint/package-lock.json" \
		"$root/tools/markdownlint/package-lock.json"
	git -C "$root" init -q
	printf '%s\n' '.workingdir/' >"$root/.gitignore"
	printf '%s\n' "$root"
}

expect_failure() {
	local pattern="$1"
	shift
	local output
	local status=0
	output="$("$@" 2>&1)" || status=$?
	if (( status == 0 )); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
	if [[ "$output" != *"$pattern"* ]]; then
		printf 'missing failure %q in: %s\n' "$pattern" "$output" >&2
		return 1
	fi
}

positive_root="$(new_repo positive)"
printf '# Clean\n\nBody.\n' >"$positive_root/docs/clean.md"
git -C "$positive_root" add .
bash "$positive_root/scripts/ci/markdownlint.sh" --root "$positive_root" >/dev/null

negative_root="$(new_repo negative)"
printf '# Broken\nBody.\n' >"$negative_root/docs/broken.md"
git -C "$negative_root" add .
expect_failure 'MD022' bash "$negative_root/scripts/ci/markdownlint.sh" --root "$negative_root"

untracked_root="$(new_repo untracked)"
printf '# Clean\n\nBody.\n' >"$untracked_root/docs/clean.md"
git -C "$untracked_root" add .
printf '# Broken\nBody.\n' >"$untracked_root/docs/untracked.md"
expect_failure 'docs/untracked.md' bash "$untracked_root/scripts/ci/markdownlint.sh" --root "$untracked_root"

missing_lock_root="$(new_repo missing-lock)"
printf '# Clean\n\nBody.\n' >"$missing_lock_root/docs/clean.md"
git -C "$missing_lock_root" add .
rm "$missing_lock_root/tools/markdownlint/package-lock.json"
expect_failure 'missing Markdownlint package lock' \
	bash "$missing_lock_root/scripts/ci/markdownlint.sh" --root "$missing_lock_root"

excluded_root="$(new_repo excluded)"
mkdir -p "$excluded_root/.agents/agents" "$excluded_root/.workingdir" \
	"$excluded_root/docs/upstream"
printf '# Broken\nBody.\n' >"$excluded_root/AGENTS.md"
printf '# Broken\nBody.\n' >"$excluded_root/CHANGELOG.md"
printf '# Broken\nBody.\n' >"$excluded_root/.agents/agents/generated.md"
printf '# Broken\nBody.\n' >"$excluded_root/docs/upstream/vendor.md"
printf '# Broken\nBody.\n' >"$excluded_root/.workingdir/ignored.md"
git -C "$excluded_root" add .
bash "$excluded_root/scripts/ci/markdownlint.sh" --root "$excluded_root" >/dev/null

symlink_root="$(new_repo symlink)"
printf '# Clean\n\nBody.\n' >"$suite_root/outside.md"
ln -s "$suite_root/outside.md" "$symlink_root/docs/external.md"
git -C "$symlink_root" add .
expect_failure 'refuses symlink input' bash "$symlink_root/scripts/ci/markdownlint.sh" --root "$symlink_root"

printf 'markdownlint policy tests passed\n'
