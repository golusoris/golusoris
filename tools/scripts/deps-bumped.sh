#!/usr/bin/env bash
# Emit a "Dependencies bumped" markdown section by diffing go.mod
# `require` blocks between two refs. See PLAN.md §10.
#
# Usage:
#   tools/scripts/deps-bumped.sh <from-ref> <to-ref>
#
# Output: markdown on stdout. Caller appends to release notes.

set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: $0 <from-ref> <to-ref>" >&2
  exit 2
fi

FROM="$1"
TO="$2"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

extract() {
  local ref="$1"
  local out="$2"
  git show "${ref}:go.mod" 2>/dev/null \
    | awk '
      /^require \(/ { in_block = 1; next }
      /^\)/         { in_block = 0; next }
      in_block && NF >= 2 && $1 !~ /^\/\// {
        # line: "<module> <version> [// indirect]"
        mod = $1; ver = $2
        gsub(/[[:space:]]*\/\/.*$/, "", ver)
        print mod " " ver
      }
      /^require [^(]/ {
        # single-line require: "require <module> <version>"
        print $2 " " $3
      }
    ' | sort -u > "$out"
}

extract "$FROM" "$TMPDIR/from.txt"
extract "$TO"   "$TMPDIR/to.txt"

echo "### Dependencies bumped"
echo

# Join on module name, emit "bumped" / "added" / "removed".
join -j 1 -a 1 -a 2 -e '-' -o '0,1.2,2.2' "$TMPDIR/from.txt" "$TMPDIR/to.txt" \
  | awk '
    $2 == "-" { printf("- %s: _added_ %s\n", $1, $3); next }
    $3 == "-" { printf("- %s: _removed_ (was %s)\n", $1, $2); next }
    $2 != $3  { printf("- %s: %s → %s\n", $1, $2, $3) }
  '
