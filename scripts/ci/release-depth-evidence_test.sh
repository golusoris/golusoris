#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly script="$repo_root/scripts/ci/release-depth-evidence.sh"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-depth-evidence.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

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

expect_status() {
	local want="$1" expected="$2" output status=0
	shift 2
	output="$("$@" 2>&1)" || status=$?
	if ((status != want)); then
		printf 'expected status %d, got %d: %s\n%s\n' "$want" "$status" "$*" "$output" >&2
		return 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing %q in: %s\n' "$expected" "$output" >&2
		return 1
	fi
}

g() {
	local dir="$1"
	shift
	git -C "$dir" -c commit.gpgsign=false -c tag.gpgsign=false "$@"
}

new_repo() {
	local dir="$suite_root/$1"
	mkdir -p "$dir"
	git init -q "$dir"
	g "$dir" config user.name tester
	g "$dir" config user.email tester@example.test
	printf '%s\n' "$dir"
}

# commit makes one commit with the subject $2 touching the file $3 (default file.txt).
commit() {
	local dir="$1" subject="$2" path="${3:-file.txt}"
	mkdir -p "$dir/$(dirname "$path")"
	printf '%s\n' "$subject" >>"$dir/$path"
	g "$dir" add -A
	g "$dir" commit -q -m "$subject"
}

tag() {
	g "$1" tag "$2"
}

sha7() {
	g "$1" rev-parse --short=7 "$2"
}

# evidence writes a complete evidence set for $2 (directory name) over base $3; rows come from stdin.
evidence() {
	local dir="$1" name="$2" base="$3" target
	target="$dir/.config/release-evidence/$name"
	mkdir -p "$target"
	{
		printf '# base: %s\n' "$base"
		cat
	} >"$target/e2e.tsv"
	printf 'from: %s\nresult: PASS\n' "$base" >"$target/upgrade.log"
	printf 'result: PASS\n' >"$target/dogfood.log"
}

row() {
	printf '%s\t#1\tTestX\t%s\t%s\n' "$(sha7 "$1" "$2")" "${3:-PASS}" "${4:-}"
}

# standard builds v0.1.0, then two user-visible and five other commits, and returns the repository.
standard() {
	local dir
	dir="$(new_repo "$1")"
	commit "$dir" 'chore: init'
	tag "$dir" v0.1.0
	commit "$dir" 'feat(api): add a thing (#2)'
	g "$dir" tag feat-commit
	commit "$dir" 'fix(deps): bump a library (#3)'
	g "$dir" tag fix-commit
	commit "$dir" 'docs: explain it'
	commit "$dir" 'chore: tidy'
	commit "$dir" 'ci: speed up'
	commit "$dir" 'test: more cases'
	printf '%s\n' "$dir"
}

complete_evidence() {
	local dir="$1" name="${2:-v0.2.0}" base="${3:-v0.1.0}"
	{
		row "$dir" feat-commit
		row "$dir" fix-commit
	} | evidence "$dir" "$name" "$base"
}

run() {
	local dir="$1"
	shift
	bash "$script" --root "$dir" "$@"
}

# positive: a minor release with complete evidence
repo="$(standard minor-ok)"
complete_evidence "$repo"
tag "$repo" v0.2.0
expect_status 0 'has depth evidence over v0.1.0..' run "$repo" check v0.2.0
# the same range checked before the tag exists
g "$repo" tag -d v0.2.0 >/dev/null
expect_status 0 'has depth evidence' run "$repo" check v0.2.0 HEAD
expect_failure 'name the end-ref' run "$repo" check v0.2.0

# positive: a patch release needs no evidence
repo="$(standard patch)"
tag "$repo" v0.1.1
expect_status 0 'v0.1.1 is a patch release; no depth evidence required' run "$repo" check v0.1.1

# negative: evidence directory missing
repo="$(standard no-dir)"
tag "$repo" v0.2.0
expect_failure 'evidence directory .config/release-evidence/v0.2.0 is missing' run "$repo" check v0.2.0

# negative: each of the three files missing
for gone in e2e.tsv upgrade.log dogfood.log; do
	repo="$(standard "no-$gone")"
	complete_evidence "$repo"
	rm "$repo/.config/release-evidence/v0.2.0/$gone"
	tag "$repo" v0.2.0
	expect_failure ".config/release-evidence/v0.2.0/$gone is missing" run "$repo" check v0.2.0
done

# negative: a user-visible commit without a row names its sha7
repo="$(standard missing-row)"
row "$repo" feat-commit | evidence "$repo" v0.2.0 v0.1.0
tag "$repo" v0.2.0
missing="$(sha7 "$repo" fix-commit)"
expect_failure "user-visible commit $missing" run "$repo" check v0.2.0
if run "$repo" check v0.2.0 2>&1 | grep -q "$(sha7 "$repo" feat-commit)"; then
	printf 'a covered commit was reported as missing\n' >&2
	exit 1
fi

# boundary: docs, chore, ci and test commits are not required; a release commit is not either
repo="$(standard release-commit)"
commit "$repo" 'chore: release main (#9)'
complete_evidence "$repo"
tag "$repo" v0.2.0
expect_status 0 'has depth evidence' run "$repo" check v0.2.0

# boundary: a breaking footer makes a docs commit user-visible; a refactor and a ! subject too
repo="$(standard breaking)"
g "$repo" commit -q --allow-empty -m 'docs: reshape' -m 'BREAKING CHANGE: the shape moved'
g "$repo" tag breaking-commit
commit "$repo" 'refactor!: drop a field'
g "$repo" tag bang-commit
{
	row "$repo" feat-commit
	row "$repo" fix-commit
} | evidence "$repo" v0.2.0 v0.1.0
tag "$repo" v0.2.0
expect_failure "user-visible commit $(sha7 "$repo" breaking-commit)" run "$repo" check v0.2.0
expect_failure "user-visible commit $(sha7 "$repo" bang-commit)" run "$repo" check v0.2.0
{
	row "$repo" feat-commit
	row "$repo" fix-commit
	row "$repo" breaking-commit
	row "$repo" bang-commit
} | evidence "$repo" v0.2.0 v0.1.0
expect_status 0 'has depth evidence' run "$repo" check v0.2.0

# negative: a FAIL row
repo="$(standard fail-row)"
{
	row "$repo" feat-commit
	row "$repo" fix-commit FAIL boom
} | evidence "$repo" v0.2.0 v0.1.0
tag "$repo" v0.2.0
expect_failure 'FAIL' run "$repo" check v0.2.0

# negative and positive: SKIP without and with a reason
repo="$(standard skip-empty)"
{
	row "$repo" feat-commit
	row "$repo" fix-commit SKIP
} | evidence "$repo" v0.2.0 v0.1.0
tag "$repo" v0.2.0
expect_failure 'SKIP without a reason' run "$repo" check v0.2.0
{
	row "$repo" feat-commit
	row "$repo" fix-commit SKIP 'covered by the feat test'
} | evidence "$repo" v0.2.0 v0.1.0
expect_status 0 'has depth evidence' run "$repo" check v0.2.0

# negative: the base header names an untagged version taken from the CHANGELOG
repo="$(standard wrong-base)"
complete_evidence "$repo" v0.2.0 v0.1.1
tag "$repo" v0.2.0
expect_failure "'# base: v0.1.1' but the release range starts after v0.1.0" run "$repo" check v0.2.0

# negative: no base header at all
repo="$(standard no-base-header)"
complete_evidence "$repo"
sed -i '/^# base:/d' "$repo/.config/release-evidence/v0.2.0/e2e.tsv"
tag "$repo" v0.2.0
expect_failure "no '# base:' header" run "$repo" check v0.2.0

# boundary: an untagged intermediate version leaves the previous tag as the base
repo="$(new_repo intermediate)"
commit "$repo" 'chore: init'
tag "$repo" v0.1.0
commit "$repo" 'fix: first fix'
g "$repo" tag first-fix
commit "$repo" 'chore: release main (#4)'
commit "$repo" 'feat: new thing'
g "$repo" tag feat-commit
commit "$repo" 'fix: second fix'
g "$repo" tag fix-commit
# the commit before the untagged 0.1.1 is part of the range and needs its row
{
	row "$repo" feat-commit
	row "$repo" fix-commit
} | evidence "$repo" v0.2.0 v0.1.0
tag "$repo" v0.2.0
expect_failure "user-visible commit $(sha7 "$repo" first-fix)" run "$repo" check v0.2.0
{
	row "$repo" first-fix
	row "$repo" feat-commit
	row "$repo" fix-commit
} | evidence "$repo" v0.2.0 v0.1.0
expect_status 0 'over v0.1.0..' run "$repo" check v0.2.0

# boundary: the highest version tag wins, not the newest or the alphabetical one
repo="$(new_repo version-sort)"
commit "$repo" 'chore: init'
tag "$repo" v0.9.0
commit "$repo" 'chore: more'
tag "$repo" v0.10.0
commit "$repo" 'feat: next'
g "$repo" tag feat-commit
row "$repo" feat-commit | evidence "$repo" v0.11.0 v0.10.0
tag "$repo" v0.11.0
expect_status 0 'over v0.10.0..' run "$repo" check v0.11.0

# boundary: a higher tag on another branch is not an ancestor and is not the base
repo="$(new_repo side-branch)"
commit "$repo" 'chore: init'
tag "$repo" v0.1.0
g "$repo" switch -q -c side
commit "$repo" 'chore: side'
tag "$repo" v0.5.0
g "$repo" switch -q -
commit "$repo" 'feat: main thing'
g "$repo" tag feat-commit
row "$repo" feat-commit | evidence "$repo" v0.2.0 v0.1.0
tag "$repo" v0.2.0
expect_status 0 'over v0.1.0..' run "$repo" check v0.2.0

# negative: the upgrade log and the dogfood log
repo="$(standard upgrade-from)"
complete_evidence "$repo"
printf 'from: v0.0.9\nresult: PASS\n' >"$repo/.config/release-evidence/v0.2.0/upgrade.log"
tag "$repo" v0.2.0
expect_failure "upgrade.log lacks the line 'from: v0.1.0'" run "$repo" check v0.2.0
printf 'from: v0.1.0\nresult: FAIL\n' >"$repo/.config/release-evidence/v0.2.0/upgrade.log"
expect_failure "upgrade.log lacks the line 'result: PASS'" run "$repo" check v0.2.0
printf 'from: v0.1.0 and more\nresult: PASS\n' >"$repo/.config/release-evidence/v0.2.0/upgrade.log"
expect_failure "upgrade.log lacks the line 'from: v0.1.0'" run "$repo" check v0.2.0
# boundary: one leading `date -Is` stamp is accepted, anything else before the marker is not
printf '2026-11-07T00:47:22+01:00 from: v0.1.0\n2026-11-07T00:47:22Z result: PASS\n' >"$repo/.config/release-evidence/v0.2.0/upgrade.log"
expect_status 0 'has depth evidence' run "$repo" check v0.2.0
printf 'note from: v0.1.0\nresult: PASS\n' >"$repo/.config/release-evidence/v0.2.0/upgrade.log"
expect_failure "upgrade.log lacks the line 'from: v0.1.0'" run "$repo" check v0.2.0
printf '2026-11-07T00:47:22+01:00 2026-11-07T00:47:22+01:00 from: v0.1.0\nresult: PASS\n' >"$repo/.config/release-evidence/v0.2.0/upgrade.log"
expect_failure "upgrade.log lacks the line 'from: v0.1.0'" run "$repo" check v0.2.0

repo="$(standard dogfood)"
complete_evidence "$repo"
printf 'result: FAIL\n' >"$repo/.config/release-evidence/v0.2.0/dogfood.log"
tag "$repo" v0.2.0
expect_failure "dogfood.log lacks the line 'result: PASS'" run "$repo" check v0.2.0

# negative: all problems are reported, not only the first
repo="$(standard all-errors)"
mkdir -p "$repo/.config/release-evidence/v0.2.0"
printf '# base: v0.0.1\n' >"$repo/.config/release-evidence/v0.2.0/e2e.tsv"
printf 'nothing\n' >"$repo/.config/release-evidence/v0.2.0/upgrade.log"
printf 'nothing\n' >"$repo/.config/release-evidence/v0.2.0/dogfood.log"
tag "$repo" v0.2.0
if output="$(run "$repo" check v0.2.0 2>&1)"; then
	printf 'expected all-errors check to fail: %s\n' "$output" >&2
	exit 1
fi
lines="$(printf '%s\n' "$output" | grep -c '^release-depth-evidence: ')"
if ((lines < 6)); then
	printf 'expected every problem on its own line, got %d: %s\n' "$lines" "$output" >&2
	exit 1
fi

# negative: no earlier tag
repo="$(new_repo first-release)"
commit "$repo" 'feat: first'
tag "$repo" v1.0.0
expect_failure 'no base for v1.0.0' run "$repo" check v1.0.0

# negative: a major release is checked like a minor one
repo="$(standard major)"
tag "$repo" v1.0.0
expect_failure 'evidence directory .config/release-evidence/v1.0.0 is missing' run "$repo" check v1.0.0

# negative: bad arguments and tags
expect_status 2 'usage:' run "$repo" check
expect_status 2 'usage:' run "$repo" check v1.0.0 HEAD extra
expect_status 2 'usage:' run "$repo" frobnicate
expect_status 2 'not a release tag' run "$repo" check main
expect_failure 'does not resolve to a commit' run "$repo" check v1.0.0 no-such-ref

# core component
core_repo() {
	local dir
	dir="$(new_repo "$1")"
	commit "$dir" 'chore: init'
	commit "$dir" 'chore: core init' core/a.go
	tag "$dir" v0.1.0
	tag "$dir" core/v0.1.0
	commit "$dir" 'feat: root only' root.go
	g "$dir" tag root-commit
	commit "$dir" 'fix(core): core fix' core/a.go
	g "$dir" tag core-commit
	printf '%s\n' "$dir"
}

repo="$(core_repo core-outside)"
row "$repo" core-commit | evidence "$repo" core-v0.2.0 core/v0.1.0
tag "$repo" core/v0.2.0
expect_status 0 'core/v0.2.0 has depth evidence over core/v0.1.0..' run "$repo" check core/v0.2.0

repo="$(core_repo core-inside)"
row "$repo" root-commit | evidence "$repo" core-v0.2.0 core/v0.1.0
tag "$repo" core/v0.2.0
expect_failure "user-visible commit $(sha7 "$repo" core-commit)" run "$repo" check core/v0.2.0

# boundary: a root tag is not a core base and a core tag is not a root base
repo="$(core_repo core-base)"
tag "$repo" core/v0.1.5
{
	row "$repo" root-commit
	row "$repo" core-commit
} | evidence "$repo" v0.2.0 v0.1.0
tag "$repo" v0.2.0
expect_status 0 'over v0.1.0..' run "$repo" check v0.2.0

# pending
manifest() {
	printf '{\n  ".": "%s",\n  "core": "%s"\n}\n' "$2" "$3" >"$1/.release-please-manifest.json"
}

repo="$(standard pending-skip)"
tag "$repo" v0.2.0
tag "$repo" core/v0.2.0
manifest "$repo" 0.2.0 0.2.0
expect_status 0 'v0.2.0 already released; skipped' run "$repo" pending

repo="$(standard pending-patch)"
manifest "$repo" 0.1.1 0.1.1
expect_status 0 'v0.1.1 is a patch release' run "$repo" pending

repo="$(standard pending-minor)"
tag "$repo" core/v0.1.0
manifest "$repo" 0.2.0 0.1.0
expect_failure 'evidence directory .config/release-evidence/v0.2.0 is missing' run "$repo" pending

repo="$(standard pending-evidence)"
tag "$repo" core/v0.1.0
manifest "$repo" 0.2.0 0.1.0
complete_evidence "$repo"
expect_status 0 'v0.2.0 has depth evidence' run "$repo" pending
expect_status 2 'usage:' run "$repo" pending extra
expect_failure 'no .release-please-manifest.json' run "$(new_repo pending-none)" pending

# one component released and the other pending: the pending one decides
repo="$(standard pending-mixed)"
tag "$repo" v0.2.0
manifest "$repo" 0.2.0 0.2.0
expect_failure 'core/v0.2.0' run "$repo" pending

printf 'release-depth-evidence_test: ok\n'
