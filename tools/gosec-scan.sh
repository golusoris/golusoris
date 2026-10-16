#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

if (($# == 0)); then
  printf 'usage: gosec-scan.sh <gosec binary> [gosec args...]\n' >&2
  exit 2
fi

for arg in "$@"; do
  if [[ "$arg" == -quiet ]]; then
    printf '%s\n' 'gosec-scan: -quiet hides the file count this guard reads' >&2
    exit 2
  fi
done

output="$(mktemp "${TMPDIR:-/tmp}/golusoris-gosec.XXXXXX")"
trap 'rm -f -- "$output"' EXIT

status=0
"$@" >"$output" || status=$?
cat "$output"

# gosec colours the summary; strip SGR escapes in the C locale so ranges stay valid.
files="$(LC_ALL=C sed -E $'s/\033\\[[0-9;]*m//g' "$output" |
  LC_ALL=C awk '/^[[:space:]]*Files[[:space:]]*:[[:space:]]*[0-9]+/ { sub(/^[^:]*:[[:space:]]*/, ""); sub(/[^0-9].*$/, ""); files = $0 } END { print files }')"
if [[ -z "$files" || "$files" =~ ^0+$ ]]; then
  printf 'gosec-scan: gosec scanned no Go file in %s; run it in the directory that holds go.mod\n' "$PWD" >&2
  exit 1
fi

exit "$status"
