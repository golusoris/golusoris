#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: fail when any staged .go file is not gofumpt-formatted.
set -euo pipefail
# shellcheck source=scripts/hooks/lib.sh
. "$(dirname "$0")/lib.sh"
# shellcheck source=tools/tool-versions.env
. "$(dirname "$0")/../../tools/tool-versions.env"

declare -a files=()
if [[ "${1:-}" == "--all" ]]; then
	mapfile -d '' files < <(git ls-files -z -- '*.go')
	declare -a live_files=()
	for file in "${files[@]}"; do
		[[ -f "$file" ]] && live_files+=("$file")
	done
	files=("${live_files[@]}")
else
	mapfile -t files < <(go_files "$@")
fi

((${#files[@]} > 0)) || skip "no Go files"
need_tool gofumpt "go install mvdan.cc/gofumpt@${GOFUMPT_VERSION}"

actual_version=$(gofumpt -version)
case "$actual_version" in
	"$GOFUMPT_VERSION" | "$GOFUMPT_VERSION "*) ;;
	*) fail "gofumpt ${GOFUMPT_VERSION} is required; found ${actual_version} (install: go install mvdan.cc/gofumpt@${GOFUMPT_VERSION})" ;;
esac

unformatted=$(gofumpt -l "${files[@]}")
[ -z "$unformatted" ] || fail "not gofumpt-formatted (run: gofumpt -w <file>):
$unformatted"
