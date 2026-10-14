#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly smoke="$repo_root/scripts/ci/release-smoke.sh"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-release-smoke.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

# stub writes an executable that prints its first argument and exits with the second.
stub() {
	local path="$1" output="$2" status="${3:-0}"
	mkdir -p "$(dirname "$path")"
	printf '#!/usr/bin/env bash\nprintf "%%s\\n" %q\nexit %d\n' "$output" "$status" >"$path"
	chmod +x "$path"
}

# fixture builds a dist/ with one linux/amd64 and one darwin/arm64 entry per binary.
fixture() {
	local dir="$1" core_out="$2" mcp_out="$3"
	rm -rf "$dir"
	mkdir -p "$dir/dist"
	stub "$dir/dist/golusoris_linux_amd64_v1/golusoris" "$core_out"
	stub "$dir/dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp" "$mcp_out"
	cat >"$dir/dist/artifacts.json" <<'JSON'
[
 {"name":"golusoris","path":"dist/golusoris_linux_amd64_v1/golusoris","goos":"linux","goarch":"amd64","type":"Binary","extra":{"ID":"golusoris"}},
 {"name":"golusoris","path":"dist/golusoris_darwin_arm64_v8.0/golusoris","goos":"darwin","goarch":"arm64","type":"Binary","extra":{"ID":"golusoris"}},
 {"name":"golusoris-mcp","path":"dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp","goos":"linux","goarch":"amd64","type":"Binary","extra":{"ID":"golusoris-mcp"}},
 {"name":"golusoris_1.2.3_linux_amd64.tar.gz","path":"dist/golusoris_1.2.3_linux_amd64.tar.gz","goos":"linux","goarch":"amd64","type":"Archive","extra":{"ID":"default"}}
]
JSON
}

run_in() {
	local dir="$1"
	shift
	(cd "$dir" && "$smoke" "$@")
}

expect_failure() {
	local pattern="$1"
	shift
	local output status=0
	output="$(run_in "$@" 2>&1)" || status=$?
	if ((status == 0)) || [[ "$output" != *"$pattern"* ]]; then
		printf 'expected failure containing %q, got status %d: %s\n' "$pattern" "$status" "$output" >&2
		exit 1
	fi
}

good_core='golusoris version 1.2.3 (commit abc1234, built 2026-10-13T00:00:00Z)'
good_mcp='golusoris-mcp version 1.2.3'
case_dir="$suite_root/case"

fixture "$case_dir" "$good_core" "$good_mcp"
output="$(run_in "$case_dir" v1.2.3)"
[[ "$output" == *"$good_core"* && "$output" == *"$good_mcp"* ]] || {
	printf 'smoke run did not report both binaries: %s\n' "$output" >&2
	exit 1
}

fixture "$case_dir" 'golusoris version dev (commit none, built unknown)' "$good_mcp"
expect_failure 'want "golusoris version 1.2.3"' "$case_dir" v1.2.3

fixture "$case_dir" "$good_core" 'golusoris-mcp version 1.2.30'
expect_failure 'want "golusoris-mcp version 1.2.3"' "$case_dir" v1.2.3

fixture "$case_dir" "$good_core" "$good_mcp"
stub "$case_dir/dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp" 'Error: unknown flag: --version' 1
expect_failure 'golusoris-mcp --version failed' "$case_dir" v1.2.3

fixture "$case_dir" "$good_core" "$good_mcp"
jq 'map(select(.extra.ID != "golusoris-mcp"))' "$case_dir/dist/artifacts.json" >"$case_dir/a.json"
mv "$case_dir/a.json" "$case_dir/dist/artifacts.json"
expect_failure 'golusoris-mcp: expected one linux/amd64 binary, found 0' "$case_dir" v1.2.3

fixture "$case_dir" "$good_core" "$good_mcp"
jq '. + [.[0]]' "$case_dir/dist/artifacts.json" >"$case_dir/a.json"
mv "$case_dir/a.json" "$case_dir/dist/artifacts.json"
expect_failure 'golusoris: expected one linux/amd64 binary, found 2' "$case_dir" v1.2.3

fixture "$case_dir" "$good_core" "$good_mcp"
: >"$case_dir/dist/artifacts.json"
expect_failure 'missing or empty' "$case_dir" v1.2.3

fixture "$case_dir" "$good_core" "$good_mcp"
expect_failure 'empty version' "$case_dir" v
expect_failure 'usage:' "$case_dir"

printf 'release smoke tests passed\n'
