#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly REPO_ROOT
readonly CHECK="$REPO_ROOT/tools/gosec-scan.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-gosec-test.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT

new_stub() {
  local name="$1" output="$2" status="$3"
  {
    printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail'
    printf 'printf "%%s" %q\n' "$output"
    printf 'exit %q\n' "$status"
  } >"$work/$name"
  chmod +x "$work/$name"
}

passed=0
expect() {
  local want="$1" label="$2" pattern="$3"
  shift 3
  local status=0 output
  output="$("$@" 2>&1)" || status=$?
  if [[ "$status" -ne "$want" ]]; then
    printf 'FAIL %s: want exit %d, exit %d\n%s\n' "$label" "$want" "$status" "$output" >&2
    exit 1
  fi
  grep -Fq -- "$pattern" <<<"$output" || {
    printf 'FAIL %s: output lacks %q\n%s\n' "$label" "$pattern" "$output" >&2
    exit 1
  }
  passed=$((passed + 1))
}

new_stub zero $'Files  : 0\n' 0
new_stub positive $'Files  : 3\n' 0
new_stub finding $'Files  : 3\n' 1
new_stub error $'Files  : 3\n' 7
new_stub missing $'no summary\n' 0
new_stub ansi $'\033[32mFiles  : 5\033[0m\n' 0
new_stub last-zero $'Files  : 3\nFiles  : 0\n' 0
new_stub last-positive $'Files  : 0\nFiles  : 3\n' 0
new_stub padded-zero $'Files  : 000\n' 0

expect 1 'zero files' 'gosec scanned no Go file in' bash "$CHECK" "$work/zero"
expect 0 'positive count' 'Files  : 3' bash "$CHECK" "$work/positive"
expect 1 'finding status preserved' 'Files  : 3' bash "$CHECK" "$work/finding"
expect 7 'error status preserved' 'Files  : 3' bash "$CHECK" "$work/error"
expect 1 'missing summary' 'directory that holds go.mod' bash "$CHECK" "$work/missing"
expect 0 'ANSI summary' $'\033[32mFiles  : 5\033[0m' bash "$CHECK" "$work/ansi"
expect 2 'quiet rejected' '-quiet hides the file count this guard reads' bash "$CHECK" "$work/positive" -quiet
expect 2 'quiet among arguments' '-quiet hides the file count this guard reads' bash "$CHECK" "$work/positive" ./... -quiet -exclude-generated
expect 2 'missing binary argument' 'usage: gosec-scan.sh' bash "$CHECK"
expect 1 'last summary zero' 'gosec scanned no Go file in' bash "$CHECK" "$work/last-zero"
expect 0 'last summary positive' 'Files  : 3' bash "$CHECK" "$work/last-positive"
expect 1 'zero with leading zeros' 'gosec scanned no Go file in' bash "$CHECK" "$work/padded-zero"

mkdir -p "$work/module/subdir"
printf 'module example.invalid/test\n' >"$work/module/go.mod"
expect 2 'shared Makefile zero from subdir' 'gosec scanned no Go file in' \
  make -C "$work/module/subdir" -f "$REPO_ROOT/tools/Makefile.shared" gosec "GOSEC=$work/zero"
expect 0 'shared Makefile positive from subdir' 'Files  : 3' \
  make -C "$work/module/subdir" -f "$REPO_ROOT/tools/Makefile.shared" gosec "GOSEC=$work/positive"

printf '%d cases passed\n' "$passed"
