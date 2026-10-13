#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# shellcheck disable=SC2016 # fixtures hold literal GitHub expressions, never shell expansions.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly policy="$repo_root/scripts/ci/sarif-upload-policy.py"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-sarif-policy.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

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

# fixture <name> <continue-on-error line or empty>
fixture() {
	local dir="$suite_root/$1"
	mkdir -p "$dir"
	{
		printf '%s\n' 'jobs:' '  scan:' '    steps:' '      - name: Upload SARIF' \
			'        uses: github/codeql-action/upload-sarif@0000000000000000000000000000000000000000 # v4' \
			'        with:' '          sarif_file: out.sarif'
		[[ -z "$2" ]] || printf '        %s\n' "$2"
		printf '%s\n' '      - name: Later step' '        continue-on-error: true' '        run: "true"'
	} >"$dir/ci.yml"
	printf '%s\n' "$dir"
}

python3 -B "$policy" "$(fixture plain '')" >/dev/null
python3 -B "$policy" "$(fixture private 'continue-on-error: ${{ github.event.repository.private }}')" >/dev/null
python3 -B "$policy" "$(fixture off 'continue-on-error: false')" >/dev/null
expect_failure 'SARIF upload has continue-on-error true' \
	python3 -B "$policy" "$(fixture open 'continue-on-error: true   # reason')"
expect_failure 'SARIF upload has continue-on-error ${{ always() }}' \
	python3 -B "$policy" "$(fixture expr 'continue-on-error: ${{ always() }}')"
mkdir -p "$suite_root/empty"
expect_failure 'no workflow files' python3 -B "$policy" "$suite_root/empty"
mkdir -p "$suite_root/none"
printf '%s\n' 'jobs: {}' >"$suite_root/none/ci.yml"
expect_failure 'no upload-sarif step' python3 -B "$policy" "$suite_root/none"
python3 -B "$policy" "$repo_root/.github/workflows" >/dev/null

printf 'sarif upload policy: ok\n'
