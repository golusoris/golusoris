#!/usr/bin/env bash
# Render per-PR changelog fragments under changelog.d/<section>/*.md into the
# "## [Unreleased]" block of CHANGELOG.md.
#
# Why fragments: editing CHANGELOG.md directly makes every concurrent module PR
# fight a merge conflict on the same section-header lines. One fragment file per
# PR is per-path, so two PRs in flight never collide. release-please rolls the
# Unreleased block into a versioned section at release time; this renderer keeps
# the block in sync between releases.
#
# Sections render in Keep-a-Changelog order:
#   Added -> Changed -> Deprecated -> Removed -> Fixed -> Security
# Each fragment is a stand-alone Markdown bullet (or block of bullets) — the same
# text you would have pasted into CHANGELOG.md.
#
# Inputs:
#   $REPO_ROOT/changelog.d/<section>/*.md   per-PR fragments (dotfiles skipped)
#
# Modes:
#   --check   Validate fragments are well-formed, then compare the rendered body
#             against the in-tree CHANGELOG.md "## [Unreleased]" block. Exit
#             non-zero on drift (CI gate). With no fragments present it still
#             succeeds as long as the block is empty (or matches).
#   --write   Splice the rendered body into the "## [Unreleased]" block of
#             CHANGELOG.md in place.
#   (no flag) Print the rendered Unreleased body to stdout and exit.
#
# Splice contract: the Unreleased block lives between the "## [Unreleased]"
# header and the *next bracketed* "## [..." header (a release-please release
# heading such as "## [0.1.0] — date"). The boundary regex is `^## \[` — NOT
# `^## ` — because fragment bodies may legitimately contain `## ` subheadings;
# a bare `^## ` sentinel would treat those as boundaries and corrupt the splice.
#
# SPDX-License-Identifier: MIT

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd)"
FRAG_ROOT="$REPO_ROOT/changelog.d"
CHANGELOG="$REPO_ROOT/CHANGELOG.md"

# Keep-a-Changelog section order: Added/Changed/Deprecated/Removed/Fixed/Security.
SECTIONS=(added changed deprecated removed fixed security)
SECTION_TITLES=(Added Changed Deprecated Removed Fixed Security)

usage() {
  sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
}

# Warn (do not fail) on fragments under subdirs the renderer does not know
# about, so a stray directory's fragments are not silently dropped.
warn_unknown_subdirs() {
  local known_csv unknown d
  known_csv="$(printf '%s\n' "${SECTIONS[@]}" | LC_ALL=C sort | paste -sd, -)"
  unknown="$(find "$FRAG_ROOT" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null |
    LC_ALL=C sort |
    awk -v known="$known_csv" 'BEGIN { n = split(known, k, ","); for (i = 1; i <= n; i++) ok[k[i]] = 1 } !($0 in ok) { print }')"
  if [[ -n "$unknown" ]]; then
    while IFS= read -r d; do
      printf 'WARNING: changelog.d/%s/ is not a Keep-a-Changelog section; fragments here are SKIPPED.\n' "$d" >&2
    done <<<"$unknown"
  fi
}

# Emit one fragment, demoting any stray leading "# "/"## " header to a bold
# pseudo-header. The renderer owns the "### Section" heading; in-fragment
# headers would pollute the visual tree and break the `^## \[` splice contract.
emit_fragment() {
  local frag="$1"
  awk '
    /^# / { sub(/^# /, ""); printf "**%s**\n", $0; next }
    /^## / { sub(/^## /, ""); printf "**%s**\n", $0; next }
    { print }
  ' "$frag"
}

# Print non-zero only when a fragment is structurally invalid (e.g. unreadable).
# Empty/whitespace-only fragments warn and are skipped, not fatal.
lint_fragments() {
  local rc=0 dir frag
  for section in "${SECTIONS[@]}"; do
    dir="$FRAG_ROOT/$section"
    [[ -d "$dir" ]] || continue
    while IFS= read -r frag; do
      [[ -n "$frag" ]] || continue
      if [[ ! -r "$frag" ]]; then
        printf 'ERROR: %s is not readable.\n' "${frag#"$REPO_ROOT"/}" >&2
        rc=1
        continue
      fi
      if [[ ! -s "$frag" ]] || ! grep -q '[^[:space:]]' "$frag"; then
        printf 'WARNING: %s is empty; it will be skipped.\n' "${frag#"$REPO_ROOT"/}" >&2
      fi
    done < <(find "$dir" -maxdepth 1 -type f -name '*.md' ! -name '.*' | LC_ALL=C sort)
  done
  return "$rc"
}

# Build the Unreleased body (without the "## [Unreleased]" header). One
# "### Section" heading per Keep-a-Changelog category that has fragments;
# sections with no fragments are skipped entirely.
render() {
  local section title dir frag first_in_section
  warn_unknown_subdirs
  for i in "${!SECTIONS[@]}"; do
    section="${SECTIONS[$i]}"
    title="${SECTION_TITLES[$i]}"
    dir="$FRAG_ROOT/$section"
    [[ -d "$dir" ]] || continue
    local files
    mapfile -t files < <(find "$dir" -maxdepth 1 -type f -name '*.md' ! -name '.*' | LC_ALL=C sort)
    [[ ${#files[@]} -gt 0 ]] || continue
    first_in_section=1
    for frag in "${files[@]}"; do
      # Skip empty/whitespace-only fragments (lint already warned).
      if [[ ! -s "$frag" ]] || ! grep -q '[^[:space:]]' "$frag"; then
        continue
      fi
      if [[ $first_in_section -eq 1 ]]; then
        printf '### %s\n\n' "$title"
        first_in_section=0
      fi
      # emit_fragment always terminates its output with a newline; follow it
      # with exactly one blank line so consecutive fragments are separated by a
      # single blank line (no MD012 multiple-blank-lines noise).
      emit_fragment "$frag"
      printf '\n'
    done
  done
}

mode="render"
case "${1:-}" in
  --check) mode="check" ;;
  --write) mode="write" ;;
  --help | -h)
    usage
    exit 0
    ;;
  "") ;;
  *)
    printf 'unknown flag: %s\n' "$1" >&2
    exit 64
    ;;
esac

# Validate fragment hygiene up front for every mode that touches CHANGELOG.md.
if [[ "$mode" != render ]]; then
  if [[ ! -f "$CHANGELOG" ]]; then
    printf 'ERROR: %s not found.\n' "$CHANGELOG" >&2
    exit 1
  fi
  if ! grep -q '^## \[Unreleased\]' "$CHANGELOG"; then
    printf 'ERROR: CHANGELOG.md has no "## [Unreleased]" header to splice into.\n' >&2
    exit 1
  fi
  if ! lint_fragments; then
    printf '\nFragment validation failed; fix the errors above.\n' >&2
    exit 1
  fi
fi

# rendered keeps no trailing blank lines (command substitution strips them);
# we normalise both sides to a single trailing newline before comparing.
rendered="$(render)"

if [[ "$mode" == render ]]; then
  if [[ -n "$rendered" ]]; then
    printf '%s\n' "$rendered"
  fi
  exit 0
fi

# Extract the current Unreleased block (body only, header excluded). The block
# ends at the next bracketed "## [" header — see the splice contract above.
current_block="$(awk '
    /^## \[Unreleased\]/ { in_block = 1; next }
    in_block && /^## \[/ { in_block = 0 }
    in_block { print }
' "$CHANGELOG")"

# Trim leading/trailing blank lines from a block so cosmetic spacing around the
# header (release-please leaves one blank line) does not register as drift.
trim_blanks() {
  awk '
    { lines[NR] = $0 }
    END {
      start = 1; end = NR
      while (start <= end && lines[start] ~ /^[[:space:]]*$/) start++
      while (end >= start && lines[end] ~ /^[[:space:]]*$/) end--
      for (i = start; i <= end; i++) print lines[i]
    }
  '
}

if [[ "$mode" == check ]]; then
  cur_trimmed="$(printf '%s\n' "$current_block" | trim_blanks)"
  ren_trimmed="$(printf '%s\n' "$rendered" | trim_blanks)"
  if [[ "$cur_trimmed" == "$ren_trimmed" ]]; then
    exit 0
  fi
  printf 'CHANGELOG.md "## [Unreleased]" block is out of sync with changelog.d/.\n\n' >&2
  diff -u <(printf '%s\n' "$cur_trimmed") <(printf '%s\n' "$ren_trimmed") >&2 || true
  printf '\nRun: scripts/changelog/render.sh --write\n' >&2
  exit 1
fi

# --write: replace the Unreleased block in place. Two awk passes — first drop the
# old block body, then re-inject the rendered body right after the header. The
# rendered text is passed via a temp file so it never has to fit in argv.
tmp_body="$(mktemp)"
tmp_out="$(mktemp)"
trap 'rm -f "$tmp_body" "$tmp_out"' EXIT

{
  printf '\n'
  if [[ -n "$rendered" ]]; then
    printf '%s\n' "$rendered"
  fi
} >"$tmp_body"

awk '
    /^## \[Unreleased\]/ { print; in_block = 1; next }
    in_block && /^## \[/ { in_block = 0 }
    !in_block { print }
' "$CHANGELOG" | awk -v body="$tmp_body" '
    /^## \[Unreleased\]/ {
        print
        while ((getline line < body) > 0) print line
        close(body)
        next
    }
    { print }
' >"$tmp_out"

# Refuse to overwrite if the header vanished (guards against template drift
# silently producing an unchanged file with no caller signal).
if ! grep -q '^## \[Unreleased\]' "$tmp_out"; then
  printf 'ERROR: rendered CHANGELOG.md missing "## [Unreleased]" header — aborting overwrite.\n' >&2
  exit 1
fi

mv "$tmp_out" "$CHANGELOG"
printf 'CHANGELOG.md "## [Unreleased]" block rewritten from changelog.d/.\n' >&2
