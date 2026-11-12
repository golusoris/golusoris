#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
# CHECK and HOOK may name mutated copies, to prove that these cases notice a weakened check.
readonly CHECK="${CHECK:-$SCRIPT_DIR/check-merge-comments.sh}"
readonly HOOK="${HOOK:-$REPO_ROOT/scripts/hooks/merge-comments.sh}"
repo="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-merge-comments.XXXXXX")"
readonly repo
trap 'rm -rf "$repo"' EXIT

git_in() {
	git -C "$repo" -c commit.gpgSign=false -c commit.cleanup=default "$@"
}

commit_as() {
	local name="$1" email="$2" message="$3"
	git_in -c user.name="$name" -c user.email="$email" commit -q --allow-empty --cleanup=whitespace -m "$message"
	git_in rev-parse HEAD
}

expect_status() {
	local want="$1" status=0 out=""
	shift
	out="$(cd "$repo" && bash "$CHECK" "$@" 2>&1)" || status="$?"
	if [[ "$status" -ne "$want" ]]; then
		printf 'check-merge-comments.sh %s: expected exit %d, got %d (%s)\n' "$*" "$want" "$status" "$out" >&2
		exit 1
	fi
}

git_in init -q -b main
git_in config user.name Dev
git_in config user.email dev@example.invalid
git_in config core.hooksPath "$repo/.git/hooks"
printf 'zero\n' >"$repo/f"
git_in add f
git_in commit -q -m 'chore: base'
base="$(git_in rev-parse HEAD)"

signed="$(commit_as Dev dev@example.invalid $'feat: x\n\nSigned-off-by: Dev <dev@example.invalid>')"
expect_status 0 "$base" "$signed"

git_in checkout -q -b clean-side "$base"
printf 'side\n' >"$repo/side.txt"
git_in add side.txt
git_in commit -q -m 'feat: side file'
git_in checkout -q -b clean-merge "$signed"
git_in merge -q --no-ff --no-edit clean-side
clean_merge="$(git_in rev-parse HEAD)"
expect_status 0 "$base" "$clean_merge"

git_in checkout -q -b side "$base"
printf 'side\n' >"$repo/f"
git_in add f
git_in commit -q -m 'feat: edit f on side'
git_in checkout -q -b conflict-keep "$base"
printf 'main\n' >"$repo/f"
git_in add f
git_in commit -q -m 'feat: edit f on conflict-keep'
if git_in merge -q --no-ff side >/dev/null 2>&1; then
	printf 'expected merge conflict on conflict-keep\n' >&2
	exit 1
fi
printf 'resolved\n' >"$repo/f"
git_in add f
git_in commit -q --no-edit
bad_merge="$(git_in rev-parse HEAD)"
expect_status 1 "$base" "$bad_merge"
bad_out=""
bad_status=0
bad_out="$(cd "$repo" && bash "$CHECK" "$base" "$bad_merge")" || bad_status="$?"
expected_err="::error::commit $bad_merge keeps git's conflict comment lines (# Conflicts:); reword the message with editor cleanup (strip mode)"
if [[ "$bad_status" -ne 1 || "$bad_out" != "$expected_err" ]]; then
	printf 'unexpected diagnostic (exit %d): %s\n' "$bad_status" "$bad_out" >&2
	exit 1
fi

git_in checkout -q -b conflict-strip "$base"
printf 'main\n' >"$repo/f"
git_in add f
git_in commit -q -m 'feat: edit f on conflict-strip'
if git_in merge -q --no-ff side >/dev/null 2>&1; then
	printf 'expected merge conflict on conflict-strip\n' >&2
	exit 1
fi
printf 'resolved\n' >"$repo/f"
git_in add f
git_in commit -q --cleanup=strip --no-edit
stripped_merge="$(git_in rev-parse HEAD)"
expect_status 0 "$base" "$stripped_merge"

# Markdown headings share the # prefix with git comments and must not be refused.
git_in checkout -q -b heading "$base"
heading_commit="$(commit_as Dev dev@example.invalid $'docs: add template\n\n# Pull Request\n\nSigned-off-by: Dev <dev@example.invalid>')"
heading_body="$(git_in show -s --format=%B "$heading_commit")"
if ! grep -qE '^# Pull Request$' <<<"$heading_body"; then
	printf 'fixture commit lost Markdown heading\n' >&2
	exit 1
fi
expect_status 0 "$base" "$heading_commit"

expect_status 3 0000000000000000000000000000000000000000 "$signed"
expect_status 3 missing-ref "$signed"
expect_status 3
expect_status 3 "$base"
expect_status 3 "$base" "$signed" extra
expect_status 3 --unknown "$signed"

readonly zero_oid='0000000000000000000000000000000000000000'
readonly zero_oid_256='0000000000000000000000000000000000000000000000000000000000000000'
hook_out=""
hook_err=""

expect_hook_status() {
	local want="$1" stdin_data="$2" status=0
	local in_file="$repo/.git/hook.in" err_file="$repo/.git/hook.err"
	printf '%s' "$stdin_data" >"$in_file"
	hook_out="$(cd "$repo" && MERGE_COMMENTS_CHECK="$CHECK" bash "$HOOK" <"$in_file" 2>"$err_file")" || status="$?"
	hook_err="$(cat "$err_file")"
	if [[ "$status" -ne "$want" ]]; then
		printf 'merge-comments.sh (%s): expected exit %d, got %d (out=%s err=%s)\n' \
			"$stdin_data" "$want" "$status" "$hook_out" "$hook_err" >&2
		exit 1
	fi
}

git_in update-ref refs/remotes/origin/main "$base"
git_in checkout -q clean-merge

expect_hook_status 0 ""
expect_hook_status 0 "refs/heads/x $clean_merge refs/heads/x $zero_oid"$'\n'
expect_hook_status 1 "refs/heads/x $bad_merge refs/heads/x $zero_oid"$'\n'
if [[ "$hook_out" != *"$bad_merge"* ]]; then
	printf 'expected hook output to name %s, got: %s\n' "$bad_merge" "$hook_out" >&2
	exit 1
fi
expect_hook_status 1 "refs/heads/clean $clean_merge refs/heads/clean $zero_oid"$'\n'"refs/heads/bad $bad_merge refs/heads/bad $zero_oid"$'\n'
expect_hook_status 1 "refs/heads/bad $bad_merge refs/heads/bad $zero_oid"$'\n'"refs/heads/clean $clean_merge refs/heads/clean $zero_oid"$'\n'
expect_hook_status 1 "refs/heads/bad1 $bad_merge refs/heads/bad1 $zero_oid"$'\n'"refs/heads/bad2 $bad_merge refs/heads/bad2 $zero_oid"$'\n'
if [[ "$hook_out" != "$expected_err"$'\n'"$expected_err" ]]; then
	printf 'expected hook to report both bad refs, got: %s\n' "$hook_out" >&2
	exit 1
fi
expect_hook_status 0 "refs/heads/x $zero_oid refs/heads/x $zero_oid"$'\n'
expect_hook_status 0 "refs/heads/x $zero_oid_256 refs/heads/x $zero_oid_256"$'\n'
expect_hook_status 3 "refs/heads/x $clean_merge refs/heads/x"$'\n'
expect_hook_status 3 "refs/heads/x $clean_merge refs/heads/x $zero_oid extra"$'\n'
expect_hook_status 3 "refs/heads/x not-a-hex-oid refs/heads/x $zero_oid"$'\n'
expect_hook_status 3 "refs/heads/x 1111111111111111111111111111111111111111 refs/heads/x $zero_oid"$'\n'

err3_calls="$repo/.git/err3.calls"
err3_stub="$repo/.git/err3.sh"
cat >"$err3_stub" <<'EOF'
#!/usr/bin/env bash
printf 'x\n' >>"$ERR3_CALLS"
exit 3
EOF
: >"$err3_calls"
err3_rc=0
(cd "$repo" && ERR3_CALLS="$err3_calls" MERGE_COMMENTS_CHECK="$err3_stub" bash "$HOOK" <<<"refs/heads/a $clean_merge refs/heads/a $zero_oid"$'\n'"refs/heads/b $signed refs/heads/b $zero_oid") >/dev/null 2>&1 || err3_rc="$?"
if [[ "$err3_rc" -ne 3 || "$(wc -l <"$err3_calls")" -ne 1 ]]; then
	printf 'expected immediate exit 3 when check-merge-comments.sh exits 3 (rc=%d)\n' "$err3_rc" >&2
	exit 1
fi

git_in update-ref -d refs/remotes/origin/main
expect_hook_status 0 "refs/heads/x $bad_merge refs/heads/x $zero_oid"$'\n'
if [[ "$hook_err" != *"merge-comments: origin/main not found, skipping"* ]]; then
	printf 'expected skip line on stderr, got: %s\n' "$hook_err" >&2
	exit 1
fi

printf 'merge comment check tests passed\n'
