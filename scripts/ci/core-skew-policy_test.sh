#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly TEST_DIR
readonly GATE="$TEST_DIR/core-skew.sh"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-core-skew-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

# Offline: a dropped replace must fail to resolve instead of reaching a proxy.
export GOPROXY=off GOFLAGS='' GOWORK=off GOTOOLCHAIN=local

# shellcheck source=scripts/ci/core-skew.sh
source "$GATE"

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

expect_success() {
	local expected="$1"
	shift
	local output
	if ! output="$("$@" 2>&1)"; then
		printf 'expected success: %s\n%s\n' "$*" "$output" >&2
		return 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing output %q in: %s\n' "$expected" "$output" >&2
		return 1
	fi
}

# Root module requiring skewcore; with_replace adds the in-repo replace.
make_fixture() {
	local dir="$1"
	local with_replace="$2"
	mkdir -p "$dir/skewcore"
	printf '%s\n' 'module example.com/skewcore' '' 'go 1.27.0' >"$dir/skewcore/go.mod"
	printf '%s\n' 'package skewcore' '' 'func Answer() int { return 42 }' >"$dir/skewcore/core.go"
	printf '%s\n' 'module example.com/skewroot' '' 'go 1.27.0' >"$dir/go.mod"
	if [[ "$with_replace" == true ]]; then
		printf '%s\n' '' 'require example.com/skewcore v0.1.0' '' \
			'replace example.com/skewcore => ./skewcore' >>"$dir/go.mod"
		printf '%s\n' 'package skewroot' '' 'import "example.com/skewcore"' '' \
			'func Answer() int { return skewcore.Answer() }' >"$dir/root.go"
	else
		printf '%s\n' 'package skewroot' '' 'func Answer() int { return 42 }' >"$dir/root.go"
	fi
	printf '%s\n' 'package skewroot' '' 'import "testing"' '' \
		'func TestAnswer(t *testing.T) { if Answer() != 42 { t.Fatal("answer") } }' >"$dir/root_test.go"
	git -C "$dir" init -q
}

# A replace to a module version is not in-repo and stays; directory replaces are listed.
printf '%s\n' 'module example.com/listing' '' 'go 1.27.0' '' \
	'replace example.com/a => ./a' \
	'replace example.com/b => ../b' \
	'replace example.com/c => example.com/fork v1.2.3' >"$suite_root/go.mod"
listed="$(cd "$suite_root" && local_replaces | tr '\n' ' ')"
if [[ "$listed" != 'example.com/a example.com/b ' ]]; then
	printf 'local_replaces listed %q\n' "$listed" >&2
	exit 1
fi

make_fixture "$suite_root/clean" false
expect_success 'module builds and tests against the required versions' bash "$GATE" "$suite_root/clean"

# The in-repo build passes; once the replace is dropped the required version must resolve on its own.
make_fixture "$suite_root/skew" true
(cd "$suite_root/skew" && go build ./...)
expect_failure 'does not build or test against the required versions of: example.com/skewcore' \
	bash "$GATE" "$suite_root/skew"

# The gate edits a copy: the checkout keeps its replace.
if ! grep -q 'replace example.com/skewcore => ./skewcore' "$suite_root/skew/go.mod"; then
	printf 'gate modified the checkout go.mod\n' >&2
	exit 1
fi

printf 'core-skew policy tests passed\n'
