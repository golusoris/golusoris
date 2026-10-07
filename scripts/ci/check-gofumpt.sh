#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
# shellcheck source=tools/tool-versions.env disable=SC1091
. "$REPO_ROOT/tools/tool-versions.env"
# shellcheck source=scripts/ci/lib/gofumpt-exceptions.sh
. "$REPO_ROOT/scripts/ci/lib/gofumpt-exceptions.sh"

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

readonly candidate_list="$temp_dir/go-files"
declare -a candidates=()
declare -a files=()
if ! git -C "$REPO_ROOT" ls-files --cached --others --exclude-standard -z -- '*.go' >"$candidate_list"; then
	printf 'failed to discover Go files\n' >&2
	exit 1
fi
mapfile -d '' candidates <"$candidate_list"
for file in "${candidates[@]}"; do
	[[ -f "$REPO_ROOT/$file" ]] || continue
	gofumpt_exempt "$file" || files+=("$REPO_ROOT/$file")
done

((${#files[@]} > 0)) || {
	printf 'no tracked or untracked Go files found\n' >&2
	exit 1
}

unformatted="$($tool -l "${files[@]}")"
[[ -z "$unformatted" ]] || {
	printf 'not gofumpt-formatted:\n%s\n' "$unformatted" >&2
	exit 1
}
