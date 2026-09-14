# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# Shared helpers for scripts/hooks/*.sh (sourced, never executed).
# shellcheck shell=bash

hook_name="${HOOK_NAME:-$(basename "$0" .sh)}"

skip() { printf '[%s] skip: %s\n' "$hook_name" "$*" >&2; exit 0; }
fail() { printf '[%s] FAIL: %s\n' "$hook_name" "$*" >&2; exit 1; }
note() { printf '[%s] %s\n' "$hook_name" "$*" >&2; }

# need_tool <bin> <install hint> — skip (exit 0) with a clear message when absent.
need_tool() {
  command -v "$1" >/dev/null 2>&1 || skip "$1 is not on PATH — install with: $2"
}

# go_files [files...] — the given (or staged) .go files that exist, minus testdata.
go_files() {
  if [ $# -gt 0 ]; then printf '%s\n' "$@"; else git diff --cached --name-only --diff-filter=ACMR; fi |
    tr '\\' '/' | grep -E '\.go$' | grep -vE '(^|/)testdata/' |
    while IFS= read -r f; do [ -f "$f" ] && printf '%s\n' "$f"; done || true
}

# module_of <file> — nearest ancestor directory holding a go.mod ("." for root).
module_of() {
  local d
  d=$(dirname "$1")
  while [ "$d" != "." ] && [ ! -f "$d/go.mod" ]; do d=$(dirname "$d"); done
  printf '%s\n' "$d"
}

# module_pkgs <files-newline-separated> — unique "<module>\t<./pkg>" pairs.
module_pkgs() {
  local f m d pkg
  printf '%s\n' "$1" | while IFS= read -r f; do
    [ -n "$f" ] || continue
    m=$(module_of "$f")
    d=$(dirname "$f")
    if [ "$d" = "$m" ]; then pkg=.; elif [ "$m" = . ]; then pkg="./$d"; else pkg="./${d#"$m/"}"; fi
    printf '%s\t%s\n' "$m" "$pkg"
  done | sort -u
}

# run_per_module <files-newline-separated> <cmd...> — runs <cmd> ./pkg... inside each module.
run_per_module() {
  local files=$1 pairs m pkgs rc=0
  shift
  pairs=$(module_pkgs "$files")
  for m in $(printf '%s\n' "$pairs" | cut -f1 | sort -u); do
    if [ "$(go env GOOS)" != linux ] && [ "$m" = "hw/udev" ]; then
      note "$m: skip (linux-only module on $(go env GOOS))"
      continue
    fi
    if ! command -v gcc >/dev/null 2>&1 && [ "$m" = "media/3d" ]; then
      note "$m: skip (C compiler gcc absent for cgo module)"
      continue
    fi
    pkgs=$(printf '%s\n' "$pairs" | awk -F'\t' -v m="$m" '$1==m{print $2}' | tr '\n' ' ')
    note "$m: $* $pkgs"
    # shellcheck disable=SC2086 # package list is intentionally word-split
    (cd "$m" && "$@" $pkgs) || rc=1
  done
  return $rc
}
