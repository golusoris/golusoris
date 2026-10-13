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

# --- API compatibility section -------------------------------------------------------------

# A fixture repository with two modules; the stand-in for go-apidiff reports a removed function in
# core, so the real wrapper produces the output the release job hands to the notes.
api_repo="$suite_root/api-repo"
mkdir -p "$api_repo/scripts/ci" "$api_repo/core" "$suite_root/bin"
cp "$repo_root/scripts/ci/go-apidiff.sh" "$repo_root/scripts/ci/go-modules.sh" "$api_repo/scripts/ci/"
git -C "$api_repo" init -q -b main
git -C "$api_repo" config user.name fixture
git -C "$api_repo" config user.email fixture@example.invalid
printf 'module example.test/root\n\ngo 1.27.1\n' >"$api_repo/go.mod"
printf 'package root\n\nfunc Root() {}\n' >"$api_repo/root.go"
printf 'module example.test/core\n\ngo 1.27.1\n' >"$api_repo/core/go.mod"
printf 'package core\n\nfunc Core() {}\n\nfunc Gone() {}\n' >"$api_repo/core/core.go"
git -C "$api_repo" add .
git -C "$api_repo" commit -q -m old
api_old="$(git -C "$api_repo" rev-parse HEAD)"
printf 'package core\n\nfunc Core() {}\n' >"$api_repo/core/core.go"
git -C "$api_repo" commit -q -am 'remove Gone'
cat >"$suite_root/bin/go-apidiff" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$PWD" == */core && "${APIDIFF_STUB_MODE:-}" == incompatible ]]; then
	printf '\nexample.test/core\n  Incompatible changes:\n  - Gone: removed\n'
	exit 1
fi
if [[ "${APIDIFF_STUB_MODE:-}" == crash ]]; then
	exit 2
fi
STUB
chmod +x "$suite_root/bin/go-apidiff"
run_wrapper() {
	APIDIFF_STUB_MODE="$1" GO_APIDIFF_BIN="$suite_root/bin/go-apidiff" \
		bash "$api_repo/scripts/ci/go-apidiff.sh" "$api_old" HEAD
}

run_wrapper compatible >"$suite_root/api-clean.txt" 2>&1
section="$(bash "$script" api v1.0.0 --apidiff "$suite_root/api-clean.txt")"
expect_contains "$section" '## API compatibility'
expect_contains "$section" 'Compared with v1.0.0: no incompatible API changes (2 modules compared, 0 new, 2 in total).'

# The planted removal must be named, with its module and the count.
run_wrapper incompatible >"$suite_root/api-broken.txt" 2>&1
section="$(bash "$script" api v1.0.0 --apidiff "$suite_root/api-broken.txt")"
expect_contains "$section" 'Compared with v1.0.0: 1 modules have incompatible API changes (2 compared, 0 new, 0 removed).'
expect_contains "$section" '**core**'
expect_contains "$section" '  - Gone: removed'
expect_contains "$section" '<summary>Incompatible changes by module</summary>'

# A checker that dies leaves no coverage line; the section states that instead of staying empty.
if run_wrapper crash >"$suite_root/api-crashed.txt" 2>&1; then
	printf 'the wrapper accepted a crashing checker\n' >&2
	exit 1
fi
section="$(bash "$script" api v1.0.0 --apidiff "$suite_root/api-crashed.txt")"
expect_contains "$section" 'apidiff not available: the apidiff run did not finish (no module coverage line)'
: >"$suite_root/api-empty.txt"
section="$(bash "$script" api v1.0.0 --apidiff "$suite_root/api-empty.txt")"
expect_contains "$section" 'apidiff not available: the apidiff run produced no output'
section="$(bash "$script" api v1.0.0 --apidiff "$suite_root/does-not-exist.txt")"
expect_contains "$section" 'apidiff not available: the apidiff run produced no output'
section="$(bash "$script" api v1.0.0 --unavailable 'the apidiff job ended with failure')"
expect_contains "$section" '## API compatibility'
expect_contains "$section" 'apidiff not available: the apidiff job ended with failure'

# Removed modules are listed, and long reports stop being quoted after the line limit.
{
	printf '==> big: API compatibility\n\nexample.test/big\n  Incompatible changes:\n'
	for i in $(seq 1 200); do printf '  - Func%d: removed\n' "$i"; done
	printf '::warning::big contains pre-1.0 incompatible API changes\n'
	printf '==> late: API compatibility\n\nexample.test/late\n  Incompatible changes:\n  - Late: removed\n'
	printf '::warning::late contains pre-1.0 incompatible API changes\n'
	printf '::warning::Go module removed since v1.0.0: old/mod\n'
	printf 'API module coverage: checked=2 added=0 removed=1 incompatible=3 current=2\n'
} >"$suite_root/api-long.txt"
section="$(bash "$script" api v1.0.0 --apidiff "$suite_root/api-long.txt")"
expect_contains "$section" 'Compared with v1.0.0: 3 modules have incompatible API changes (2 compared, 0 new, 1 removed).'
expect_contains "$section" '  - Func118: removed'
expect_contains "$section" '**late**'
expect_contains "$section" 'Report not quoted here; the release workflow run has the full output.'
expect_contains "$section" 'Removed modules: old/mod'
if [[ "$section" == *'Func119: removed'* || "$section" == *'  - Late: removed'* ]]; then
	printf 'report lines past the limit were quoted\n' >&2
	exit 1
fi

expect_failure 'usage:' bash "$script" api v1.0.0 --apidiff
expect_failure 'usage:' bash "$script" api '' --unavailable reason
expect_failure 'usage:' bash "$script" api v1.0.0 --other value

# --- previous published release ------------------------------------------------------------

# The stand-in for gh answers the release list from GH_STUB_TAGS and applies the script's own
# filter with jq; it refuses a call that would include drafts or pre-releases.
cat >"$suite_root/bin/gh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ " $* " == *' --exclude-drafts '* && " $* " == *' --exclude-pre-releases '* ]] || {
	printf 'gh stub: drafts or pre-releases not excluded\n' >&2
	exit 1
}
filter=""
while (($# > 0)); do
	if [[ "$1" == --jq ]]; then
		filter="$2"
	fi
	shift
done
printf '%s' "$GH_STUB_TAGS" | jq -r "$filter"
STUB
chmod +x "$suite_root/bin/gh"
previous_release() {
	PATH="$suite_root/bin:$PATH" GH_STUB_TAGS="$1" \
		bash "$repo_root/scripts/ci/previous-published-release.sh" "$2"
}
tags='[{"tagName":"v1.3.0"},{"tagName":"core/v1.1.0"},{"tagName":"v1.2.0"}]'
[[ "$(previous_release "$tags" v1.4.0)" == v1.3.0 ]]
# A re-run after publication must not name the release itself, and non-root tags never count.
[[ "$(previous_release "$tags" v1.3.0)" == v1.2.0 ]]
[[ -z "$(previous_release '[{"tagName":"core/v1.1.0"}]' v1.0.0)" ]]
[[ -z "$(previous_release '[]' v1.0.0)" ]]
expect_failure 'usage:' bash "$repo_root/scripts/ci/previous-published-release.sh"

printf 'Release-notes breaking-change positive, negative and boundary tests passed\n'
