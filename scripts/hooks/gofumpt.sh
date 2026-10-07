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

# Exception (one file, gofumpt only): Praetor's byte-locked API gate asset is not
# gofumpt-clean (cordanaLLM/praetor#842). After the expiry the hook checks it again.
readonly locked_gate=tools/apicompat/gate/main.go
readonly locked_gate_expires=20261107
if (($(date +%Y%m%d) <= locked_gate_expires)); then
	declare -a checked_files=()
	for file in "${files[@]}"; do
		if [[ "$file" == "$locked_gate" ]]; then
			note "skip $file: exception until $locked_gate_expires (cordanaLLM/praetor#842)"
		else
			checked_files+=("$file")
		fi
	done
	files=("${checked_files[@]}")
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
