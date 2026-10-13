#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# The one gofumpt gate: no argument checks every tracked and untracked Go file under
# GOFUMPT_ROOT (default: the repository), arguments check only those files (the pre-commit hook).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
# shellcheck source=tools/tool-versions.env disable=SC1091
. "$REPO_ROOT/tools/tool-versions.env"
SCAN_ROOT="$(cd "${GOFUMPT_ROOT:-$REPO_ROOT}" && pwd)"
readonly SCAN_ROOT

tool=""
if command -v gofumpt >/dev/null; then
	tool="$(command -v gofumpt)"
fi
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-gofumpt.XXXXXX")"
cleanup() {
	rm -rf -- "$temp_dir"
}
trap cleanup EXIT

if [[ -z "$tool" ]] || [[ "$($tool -version)" != "$GOFUMPT_VERSION "* ]]; then
	GOBIN="$temp_dir" go install "mvdan.cc/gofumpt@${GOFUMPT_VERSION}"
	tool="$temp_dir/gofumpt"
fi

declare -a files=()
if (($# > 0)); then
	for file in "$@"; do
		[[ -f "$file" ]] || {
			printf 'not a file: %s\n' "$file" >&2
			exit 1
		}
		files+=("$file")
	done
else
	readonly candidate_list="$temp_dir/go-files"
	declare -a candidates=()
	if ! git -C "$SCAN_ROOT" ls-files --cached --others --exclude-standard -z -- '*.go' >"$candidate_list"; then
		printf 'failed to discover Go files\n' >&2
		exit 1
	fi
	mapfile -d '' candidates <"$candidate_list"
	for file in "${candidates[@]}"; do
		[[ -f "$SCAN_ROOT/$file" ]] || continue
		files+=("$SCAN_ROOT/$file")
	done
fi

((${#files[@]} > 0)) || {
	printf 'no tracked or untracked Go files found\n' >&2
	exit 1
}

unformatted="$($tool -l "${files[@]}")"
[[ -z "$unformatted" ]] || {
	printf 'not gofumpt-formatted (run: gofumpt -w <file>):\n%s\n' "$unformatted" >&2
	exit 1
}
printf 'gofumpt %s: %d Go files formatted\n' "$GOFUMPT_VERSION" "${#files[@]}"
