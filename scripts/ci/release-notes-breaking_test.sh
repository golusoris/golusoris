#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly script="$repo_root/scripts/ci/release-notes-breaking.sh"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-release-notes.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT
export GITHUB_REPOSITORY=example/framework

expect_failure() {
	local expected="$1"
	shift
	local output
	if output="$("$@" 2>&1)"; then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing diagnostic %q in: %s\n' "$expected" "$output" >&2
		return 1
	fi
}

expect_contains() {
	if [[ "$1" != *"$2"* ]]; then
		printf 'missing %q in: %s\n' "$2" "$1" >&2
		return 1
	fi
}

# entry prints one release-please style CHANGELOG entry; a third argument adds a breaking section.
entry() {
	printf '## [%s](https://example.test/compare/v%s...v%s) (2026-01-01)\n\n\n' "$1" "$2" "$1"
	if [[ -n "${3:-}" ]]; then
		printf '### ⚠ BREAKING CHANGES\n\n* %s\n\n' "$3"
	fi
	printf '### Bug Fixes\n\n* a fix in %s\n\n' "$1"
}

# fixture creates a repository root named $1 whose CHANGELOG is stdin and whose guides are the rest.
fixture() {
	local root="$suite_root/$1"
	shift
	mkdir -p "$root/docs/migrations"
	{
		printf '# Changelog\n\n'
		cat
	} >"$root/CHANGELOG.md"
	local version
	for version in "$@"; do
		printf '# Migration guide\n\n## Constructors return errors\n\ntext\n\n## Renamed options\n\ntext\n' \
			>"$root/docs/migrations/v$version.md"
	done
}

breaking_line='**auth:** return an error from NewManager ([#12](https://example.test/issues/12))'

# Fix-only newest entry: nothing is required, even with an older unguided breaking entry below it.
{
	entry 1.3.1 1.3.0
	entry 1.3.0 1.2.0 "$breaking_line"
} | fixture fix-only
bash "$script" --root "$suite_root/fix-only" check

# Newest entry breaking, guide present.
{
	entry 1.3.0 1.2.0 "$breaking_line"
	entry 1.2.0 1.1.0
} | fixture guided 1.3.0
bash "$script" --root "$suite_root/guided" check

# Newest entry breaking, no guide.
{ entry 1.3.0 1.2.0 "$breaking_line"; } | fixture unguided
expect_failure 'CHANGELOG entry 1.3.0 has breaking changes but docs/migrations/v1.3.0.md is missing' \
	bash "$script" --root "$suite_root/unguided" check

# Boundary: a guide for the neighbouring version does not count.
{ entry 1.3.0 1.2.0 "$breaking_line"; } | fixture wrong-version 1.2.0 1.3.1
expect_failure 'docs/migrations/v1.3.0.md is missing' \
	bash "$script" --root "$suite_root/wrong-version" check

printf 'no entries yet\n' | fixture empty
expect_failure 'CHANGELOG.md has no release entry' \
	bash "$script" --root "$suite_root/empty" check

# Notes for a release whose own entry is breaking.
notes="$(bash "$script" --root "$suite_root/guided" notes v1.3.0 v1.2.0)"
expect_contains "$notes" '## Breaking changes and migration'
expect_contains "$notes" 'This release contains breaking changes.'
expect_contains "$notes" "- $breaking_line"
expect_contains "$notes" '- Constructors return errors'
expect_contains "$notes" '- Renamed options'
expect_contains "$notes" 'Migration guide: https://github.com/example/framework/blob/v1.3.0/docs/migrations/v1.3.0.md'

# Notes name the earlier, never published version whose breaks this release carries first.
{
	entry 1.3.1 1.3.0
	entry 1.3.0 1.2.0 "$breaking_line"
	entry 1.2.0 1.1.0
} | fixture carried 1.3.0
notes="$(bash "$script" --root "$suite_root/carried" notes v1.3.1 v1.2.0)"
expect_contains "$notes" 'v1.3.1 is the first published release that carries the breaking changes of 1.3.0.'
expect_contains "$notes" 'Migration guide: https://github.com/example/framework/blob/v1.3.1/docs/migrations/v1.3.0.md'

# Boundary: the previous published release closes the range, so 1.3.0 is not repeated.
notes="$(bash "$script" --root "$suite_root/carried" notes v1.3.1 v1.3.0)"
if [[ -n "$notes" ]]; then
	printf 'fix-only range printed a block: %s\n' "$notes" >&2
	exit 1
fi

# A breaking entry inside the range without its guide stops the release.
# Without a previous published release every entry counts.
expect_failure 'docs/migrations/v1.3.0.md is missing' \
	bash "$script" --root "$suite_root/fix-only" notes v1.3.1
expect_failure 'CHANGELOG.md has no entry for 9.9.9' \
	bash "$script" --root "$suite_root/carried" notes v9.9.9
expect_failure 'CHANGELOG.md has no entry for the previous release 0.0.1 below 1.3.1' \
	bash "$script" --root "$suite_root/carried" notes v1.3.1 v0.0.1
expect_failure 'not a release tag' \
	bash "$script" --root "$suite_root/carried" notes main

printf 'Release-notes breaking-change positive, negative and boundary tests passed\n'
