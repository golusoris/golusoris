#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-buf-policy.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

readonly module=k8s/keda/internal/externalscalerpb
readonly gate="$repo_root/scripts/ci/buf-lint.sh"

# The breaking check compares against the unmodified tracked proto, so no case needs the network.
upstream="$suite_root/upstream"
mkdir -p "$upstream"
cp "$repo_root/$module/externalscaler.proto" "$upstream/"
export BUF_AGAINST="$upstream"

expect_failure() {
	local expected="$1"
	shift
	local output
	if output="$("$@" 2>&1)"; then
		printf 'expected failure: %s\n' "$*" >&2
		exit 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing diagnostic %q in: %s\n' "$expected" "$output" >&2
		exit 1
	fi
}

new_fixture() {
	local root="$suite_root/$1"
	mkdir -p "$root/$module"
	cp "$repo_root/$module/externalscaler.proto" "$repo_root/$module/buf.yaml" \
		"$repo_root/$module/buf.gen.yaml" "$root/$module/"
	git -C "$root" init -q
	git -C "$root" add "$module"
	printf '%s\n' "$root"
}

clean="$(new_fixture clean)"
BUF_SCAN_ROOT="$clean" bash "$gate" >/dev/null

lint="$(new_fixture lint)"
printf '%s\n' 'enum Planted {' '  ZERO = 0;' '}' >>"$lint/$module/externalscaler.proto"
expect_failure 'should be prefixed with "PLANTED_"' env BUF_SCAN_ROOT="$lint" bash "$gate"

breaking="$(new_fixture breaking)"
sed -i 's/int64 targetSize = 2;/int64 targetSize = 9;/' "$breaking/$module/externalscaler.proto"
if grep -Fq 'targetSize = 2;' "$breaking/$module/externalscaler.proto"; then
	printf 'breaking fixture did not change the field number\n' >&2
	exit 1
fi
expect_failure 'Previously present field "2"' env BUF_SCAN_ROOT="$breaking" bash "$gate"

stray="$(new_fixture stray)"
mkdir -p "$stray/api"
printf '%s\n' 'syntax = "proto3";' 'package stray.v1;' >"$stray/api/stray.proto"
git -C "$stray" add api
expect_failure 'api/stray.proto lies outside the linted module' env BUF_SCAN_ROOT="$stray" bash "$gate"

unpinned="$(new_fixture unpinned)"
sed -i '/626ded5d783108bedf647e20bbc83f8e458e02e4/d' "$unpinned/$module/buf.gen.yaml"
expect_failure 'does not name the upstream commit' env BUF_SCAN_ROOT="$unpinned" bash "$gate"

fake="$suite_root/buf"
printf '%s\n' '#!/usr/bin/env bash' 'printf "1.0.0\n"' >"$fake"
chmod +x "$fake"
expect_failure 'BUF_BIN is v1.0.0' env BUF_SCAN_ROOT="$clean" BUF_BIN="$fake" bash "$gate"

printf 'buf lint policy: ok\n'
